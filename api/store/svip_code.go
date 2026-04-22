package store

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"strings"
	"time"

	"tempmail/model"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

var (
	ErrSVIPActivationCodeInvalid         = errors.New("svip activation code invalid")
	ErrSVIPActivationCodeDisabled        = errors.New("svip activation code disabled")
	ErrSVIPActivationCodeExpired         = errors.New("svip activation code expired")
	ErrSVIPActivationCodeUsedUp          = errors.New("svip activation code used up")
	ErrSVIPActivationCodeAlreadyRedeemed = errors.New("svip activation code already redeemed")
)

type SVIPActivationCodeCreateOptions struct {
	Count        int
	Level        int
	DurationDays int
	MaxUses      int
	Note         string
	ExpiresAt    *time.Time
}

const svipActivationCodeColumns = `id, code, level, duration_days, max_uses, used_count, enabled, note, expires_at, created_at, updated_at`

func normalizeSVIPActivationCode(code string) string {
	code = strings.ToUpper(strings.TrimSpace(code))
	repl := strings.NewReplacer(" ", "", "\t", "", "\r", "", "\n", "", "－", "-", "—", "-")
	return repl.Replace(code)
}

func newSVIPActivationCodeString() (string, error) {
	const alphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	buf := make([]byte, 12)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	for i, b := range buf {
		buf[i] = alphabet[int(b)%len(alphabet)]
	}
	return fmt.Sprintf("SVIP-%s-%s-%s", string(buf[0:4]), string(buf[4:8]), string(buf[8:12])), nil
}

func scanSVIPActivationCode(row interface{ Scan(...interface{}) error }) (*model.SVIPActivationCode, error) {
	var c model.SVIPActivationCode
	if err := row.Scan(
		&c.ID, &c.Code, &c.Level, &c.DurationDays, &c.MaxUses, &c.UsedCount,
		&c.Enabled, &c.Note, &c.ExpiresAt, &c.CreatedAt, &c.UpdatedAt,
	); err != nil {
		return nil, err
	}
	return &c, nil
}

func (s *Store) GenerateSVIPActivationCodes(ctx context.Context, opts SVIPActivationCodeCreateOptions) ([]model.SVIPActivationCode, error) {
	if opts.Count <= 0 || opts.Count > 200 {
		return nil, fmt.Errorf("invalid_count")
	}
	if opts.Level <= 0 {
		opts.Level = 1
	}
	if opts.DurationDays < 0 {
		return nil, fmt.Errorf("invalid_duration_days")
	}
	if opts.MaxUses <= 0 {
		opts.MaxUses = 1
	}
	if opts.MaxUses > 1000 {
		return nil, fmt.Errorf("invalid_max_uses")
	}
	opts.Note = strings.TrimSpace(opts.Note)
	if len(opts.Note) > 160 {
		return nil, fmt.Errorf("invalid_note")
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	out := make([]model.SVIPActivationCode, 0, opts.Count)
	for len(out) < opts.Count {
		code, err := newSVIPActivationCodeString()
		if err != nil {
			return nil, err
		}
		row := tx.QueryRow(ctx, `
			INSERT INTO svip_activation_codes (code, level, duration_days, max_uses, note, expires_at)
			VALUES ($1,$2,$3,$4,$5,$6)
			RETURNING `+svipActivationCodeColumns,
			code, opts.Level, opts.DurationDays, opts.MaxUses, opts.Note, opts.ExpiresAt,
		)
		c, err := scanSVIPActivationCode(row)
		if err != nil {
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) && pgErr.Code == "23505" {
				// 极低概率随机码碰撞，重试生成下一次
				continue
			}
			return nil, err
		}
		out = append(out, *c)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return out, nil
}

func (s *Store) ListSVIPActivationCodes(ctx context.Context, page, size int) ([]model.SVIPActivationCode, int, error) {
	if page < 1 {
		page = 1
	}
	if size < 1 || size > 200 {
		size = 30
	}
	var total int
	if err := s.pool.QueryRow(ctx, `SELECT COUNT(*)::int FROM svip_activation_codes`).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := s.pool.Query(ctx, `
		SELECT `+svipActivationCodeColumns+`
		FROM svip_activation_codes
		ORDER BY created_at DESC
		LIMIT $1 OFFSET $2`,
		size, (page-1)*size,
	)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []model.SVIPActivationCode{}
	for rows.Next() {
		c, err := scanSVIPActivationCode(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, *c)
	}
	return out, total, rows.Err()
}

func (s *Store) SetSVIPActivationCodeEnabled(ctx context.Context, id uuid.UUID, enabled bool) (*model.SVIPActivationCode, error) {
	row := s.pool.QueryRow(ctx, `
		UPDATE svip_activation_codes
		SET enabled = $2, updated_at = NOW()
		WHERE id = $1
		RETURNING `+svipActivationCodeColumns,
		id, enabled,
	)
	return scanSVIPActivationCode(row)
}

func (s *Store) RedeemSVIPActivationCode(ctx context.Context, accountID uuid.UUID, rawCode string) (*model.Account, *model.SVIPActivationCode, error) {
	codeText := normalizeSVIPActivationCode(rawCode)
	if codeText == "" {
		return nil, nil, ErrSVIPActivationCodeInvalid
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, nil, err
	}
	defer tx.Rollback(ctx)

	codeRow := tx.QueryRow(ctx, `
		SELECT `+svipActivationCodeColumns+`
		FROM svip_activation_codes
		WHERE code = $1
		FOR UPDATE`,
		codeText,
	)
	code, err := scanSVIPActivationCode(codeRow)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil, ErrSVIPActivationCodeInvalid
		}
		return nil, nil, err
	}
	now := time.Now()
	if !code.Enabled {
		return nil, nil, ErrSVIPActivationCodeDisabled
	}
	if code.ExpiresAt != nil && !code.ExpiresAt.After(now) {
		return nil, nil, ErrSVIPActivationCodeExpired
	}
	if code.UsedCount >= code.MaxUses {
		return nil, nil, ErrSVIPActivationCodeUsedUp
	}

	var already bool
	if err := tx.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM svip_activation_redemptions WHERE code_id = $1 AND account_id = $2)`,
		code.ID, accountID,
	).Scan(&already); err != nil {
		return nil, nil, err
	}
	if already {
		return nil, nil, ErrSVIPActivationCodeAlreadyRedeemed
	}

	var currentLevel int
	var currentExpires *time.Time
	if err := tx.QueryRow(ctx,
		`SELECT svip_level, svip_expires_at FROM accounts WHERE id = $1 FOR UPDATE`,
		accountID,
	).Scan(&currentLevel, &currentExpires); err != nil {
		return nil, nil, err
	}

	var newExpires *time.Time
	if code.DurationDays > 0 {
		base := now
		if currentLevel > 0 {
			if currentExpires == nil {
				newExpires = nil // 已是永久 SVIP，兑换有限期码不降级
			} else if currentExpires.After(now) {
				base = *currentExpires
			}
		}
		if newExpires != nil || currentExpires != nil || currentLevel <= 0 {
			t := base.AddDate(0, 0, code.DurationDays)
			newExpires = &t
		}
	}

	if _, err := tx.Exec(ctx,
		`UPDATE accounts SET svip_level = $2, svip_expires_at = $3, updated_at = NOW() WHERE id = $1`,
		accountID, code.Level, newExpires,
	); err != nil {
		return nil, nil, err
	}
	if _, err := tx.Exec(ctx,
		`UPDATE svip_activation_codes SET used_count = used_count + 1, updated_at = NOW() WHERE id = $1`,
		code.ID,
	); err != nil {
		return nil, nil, err
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO svip_activation_redemptions (code_id, account_id, svip_expires_at) VALUES ($1,$2,$3)`,
		code.ID, accountID, newExpires,
	); err != nil {
		return nil, nil, err
	}

	var acc model.Account
	if err := tx.QueryRow(ctx,
		`SELECT `+accountColumns+` FROM accounts WHERE id = $1`,
		accountID,
	).Scan(&acc.ID, &acc.Username, &acc.APIKey, &acc.IsAdmin, &acc.IsActive, &acc.CreatedAt, &acc.UpdatedAt, &acc.LastSeenAt,
		&acc.SVIPLevel, &acc.SVIPExpiresAt, &acc.MailboxQuota, &acc.MailboxTTLMinutes,
		&acc.ExclusiveFanLevel, &acc.ExclusiveFanClaimedAt); err != nil {
		return nil, nil, err
	}
	code.UsedCount++
	if err := tx.Commit(ctx); err != nil {
		return nil, nil, err
	}
	return &acc, code, nil
}
