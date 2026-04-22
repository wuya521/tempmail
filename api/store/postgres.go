package store

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"math/big"
	"net"
	"strings"
	"time"

	"tempmail/model"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Store struct {
	pool *pgxpool.Pool
}

// New 创建带连接池的 Store（高并发核心）
func New(ctx context.Context, dsn string) (*Store, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("parse dsn: %w", err)
	}

// 连接池：不限并发，由 PgBouncer 统一管控实际 PG 连接数
        cfg.MaxConns = 500
        cfg.MinConns = 20
	cfg.MaxConnLifetime = 30 * time.Minute
	cfg.MaxConnIdleTime = 5 * time.Minute
	cfg.HealthCheckPeriod = 30 * time.Second

	// PgBouncer transaction 模式不支持 named prepared statements。
	// pgx v5 默认使用 QueryExecModeCacheStatement（会发送 Parse/Bind/Execute），
	// 多个连接复用同一个后端连接时会触发 "prepared statement already in use"。
	// 改为 SimpleProtocol：直接发送明文 SQL，完全绕过服务端 prepared statement 机制。
	cfg.ConnConfig.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("connect db: %w", err)
	}

	if err := pool.Ping(ctx); err != nil {
		return nil, fmt.Errorf("ping db: %w", err)
	}

	return &Store{pool: pool}, nil
}

func (s *Store) Close() {
	s.pool.Close()
}

// ==================== Account ====================

// accountColumns v10：统一 SELECT 列表顺序必须与 model.Account 字段顺序一致（pgx.RowToStructByPos 使用位置映射）
const accountColumns = `id, username, api_key, is_admin, is_active, created_at, updated_at, last_seen_at,
	svip_level, svip_expires_at, mailbox_quota, mailbox_ttl_minutes,
	exclusive_fan_level, exclusive_fan_claimed_at`

func (s *Store) GetAccountByAPIKey(ctx context.Context, apiKey string) (*model.Account, error) {
	var a model.Account
	err := s.pool.QueryRow(ctx,
		`SELECT `+accountColumns+` FROM accounts WHERE api_key = $1 AND is_active = TRUE`, apiKey,
	).Scan(&a.ID, &a.Username, &a.APIKey, &a.IsAdmin, &a.IsActive, &a.CreatedAt, &a.UpdatedAt, &a.LastSeenAt,
		&a.SVIPLevel, &a.SVIPExpiresAt, &a.MailboxQuota, &a.MailboxTTLMinutes,
		&a.ExclusiveFanLevel, &a.ExclusiveFanClaimedAt)
	if err != nil {
		return nil, err
	}
	return &a, nil
}

// GetAccountByAPIKeyAny 按 Key 查询（含已停用），用于区分「封禁」与「Key 错误」
func (s *Store) GetAccountByAPIKeyAny(ctx context.Context, apiKey string) (*model.Account, error) {
	var a model.Account
	err := s.pool.QueryRow(ctx,
		`SELECT `+accountColumns+` FROM accounts WHERE api_key = $1`, apiKey,
	).Scan(&a.ID, &a.Username, &a.APIKey, &a.IsAdmin, &a.IsActive, &a.CreatedAt, &a.UpdatedAt, &a.LastSeenAt,
		&a.SVIPLevel, &a.SVIPExpiresAt, &a.MailboxQuota, &a.MailboxTTLMinutes,
		&a.ExclusiveFanLevel, &a.ExclusiveFanClaimedAt)
	if err != nil {
		return nil, err
	}
	return &a, nil
}

// TouchAccountLastSeen 节流更新最近活跃（5 分钟内不重复写）
func (s *Store) TouchAccountLastSeen(ctx context.Context, accountID uuid.UUID) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE accounts SET last_seen_at = NOW(), updated_at = NOW()
		 WHERE id = $1 AND (last_seen_at IS NULL OR last_seen_at < NOW() - INTERVAL '5 minutes')`,
		accountID,
	)
	return err
}

// SetAccountActive 启用/停用（封禁仅停登录）
func (s *Store) SetAccountActive(ctx context.Context, accountID uuid.UUID, active bool) error {
	tag, err := s.pool.Exec(ctx,
		`UPDATE accounts SET is_active = $2, updated_at = NOW() WHERE id = $1`,
		accountID, active,
	)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}

func (s *Store) GetAccountByID(ctx context.Context, id uuid.UUID) (*model.Account, error) {
	var a model.Account
	err := s.pool.QueryRow(ctx,
		`SELECT `+accountColumns+` FROM accounts WHERE id = $1`, id,
	).Scan(&a.ID, &a.Username, &a.APIKey, &a.IsAdmin, &a.IsActive, &a.CreatedAt, &a.UpdatedAt, &a.LastSeenAt,
		&a.SVIPLevel, &a.SVIPExpiresAt, &a.MailboxQuota, &a.MailboxTTLMinutes,
		&a.ExclusiveFanLevel, &a.ExclusiveFanClaimedAt)
	if err != nil {
		return nil, err
	}
	return &a, nil
}

func (s *Store) CreateAccount(ctx context.Context, username string) (*model.Account, error) {
	apiKey := generateAPIKey()
	var a model.Account
	err := s.pool.QueryRow(ctx,
		`INSERT INTO accounts (username, api_key) VALUES ($1, $2)
		 RETURNING `+accountColumns,
		username, apiKey,
	).Scan(&a.ID, &a.Username, &a.APIKey, &a.IsAdmin, &a.IsActive, &a.CreatedAt, &a.UpdatedAt, &a.LastSeenAt,
		&a.SVIPLevel, &a.SVIPExpiresAt, &a.MailboxQuota, &a.MailboxTTLMinutes,
		&a.ExclusiveFanLevel, &a.ExclusiveFanClaimedAt)
	if err != nil {
		return nil, err
	}
	return &a, nil
}

func (s *Store) DeleteAccount(ctx context.Context, accountID uuid.UUID) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM accounts WHERE id = $1`, accountID)
	return err
}

// RotateAPIKey 为指定账号生成新的 API Key，旧 key 立即失效（accounts.api_key 为 UNIQUE 索引，UPDATE 后旧 key 查不到）。
// 返回新 key 的明文，调用者负责一次性返回给用户；函数内部对重复 key 的碰撞做最多 3 次重试。
func (s *Store) RotateAPIKey(ctx context.Context, accountID uuid.UUID) (string, error) {
	var lastErr error
	for i := 0; i < 3; i++ {
		newKey := generateAPIKey()
		tag, err := s.pool.Exec(ctx,
			`UPDATE accounts SET api_key = $2, updated_at = NOW() WHERE id = $1`,
			accountID, newKey,
		)
		if err != nil {
			lastErr = err
			if strings.Contains(err.Error(), "accounts_api_key_key") ||
				strings.Contains(strings.ToLower(err.Error()), "unique") {
				continue
			}
			return "", err
		}
		if tag.RowsAffected() == 0 {
			return "", pgx.ErrNoRows
		}
		return newKey, nil
	}
	if lastErr != nil {
		return "", lastErr
	}
	return "", fmt.Errorf("rotate api key: unknown error")
}

// AccountListFilter v10：列表筛选条件
//
//	Status: "" | "all" = 不限；"active" = 正常；"banned" = 已封禁；"svip" = 仅 SVIP
//	Search: 模糊匹配 username / api_key
type AccountListFilter struct {
	Status string
	Search string
}

// ListAccounts 列出账户（支持封禁状态/SVIP 过滤 + 用户名/Key 模糊搜索）
func (s *Store) ListAccounts(ctx context.Context, page, size int, filter AccountListFilter) ([]model.Account, int, error) {
	conds := []string{}
	args := []interface{}{}
	q := strings.TrimSpace(filter.Search)
	switch strings.ToLower(strings.TrimSpace(filter.Status)) {
	case "active":
		conds = append(conds, "is_active = TRUE")
	case "banned":
		conds = append(conds, "is_active = FALSE")
	case "svip":
		conds = append(conds, "svip_level > 0 AND (svip_expires_at IS NULL OR svip_expires_at > NOW())")
	}
	if q != "" {
		args = append(args, "%"+q+"%")
		conds = append(conds, fmt.Sprintf("(username ILIKE $%d OR api_key ILIKE $%d)", len(args), len(args)))
	}
	where := ""
	if len(conds) > 0 {
		where = " WHERE " + strings.Join(conds, " AND ")
	}

	var total int
	if err := s.pool.QueryRow(ctx, `SELECT COUNT(*) FROM accounts`+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	limitArgs := append([]interface{}{}, args...)
	limitArgs = append(limitArgs, size, (page-1)*size)
	sql := `SELECT ` + accountColumns + ` FROM accounts` + where +
		fmt.Sprintf(` ORDER BY created_at DESC LIMIT $%d OFFSET $%d`, len(limitArgs)-1, len(limitArgs))

	rows, err := s.pool.Query(ctx, sql, limitArgs...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	accounts, err := pgx.CollectRows(rows, pgx.RowToStructByPos[model.Account])
	if err != nil {
		return nil, 0, err
	}
	return accounts, total, nil
}

// ==================== v10：SVIP / 配额 ====================

// GrantSVIP 授予/续期 SVIP
//
//	level > 0 表示 SVIP 等级；expiresAt nil = 永久
//	返回操作后的账户快照，便于上层同步赠送券等副作用
func (s *Store) GrantSVIP(ctx context.Context, accountID uuid.UUID, level int, expiresAt *time.Time) (*model.Account, error) {
	if level <= 0 {
		return nil, fmt.Errorf("svip level must be > 0 (use RevokeSVIP to downgrade)")
	}
	_, err := s.pool.Exec(ctx,
		`UPDATE accounts SET svip_level = $2, svip_expires_at = $3, updated_at = NOW() WHERE id = $1`,
		accountID, level, expiresAt,
	)
	if err != nil {
		return nil, err
	}
	return s.GetAccountByID(ctx, accountID)
}

// RevokeSVIP 撤销 SVIP，降级为普通用户
func (s *Store) RevokeSVIP(ctx context.Context, accountID uuid.UUID) error {
	tag, err := s.pool.Exec(ctx,
		`UPDATE accounts SET svip_level = 0, svip_expires_at = NULL, updated_at = NOW() WHERE id = $1`,
		accountID,
	)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}

// SetMailboxQuota 设置账户邮箱配额（0=默认，-1=无限，正数=专属上限）
func (s *Store) SetMailboxQuota(ctx context.Context, accountID uuid.UUID, quota int) error {
	if quota < -1 {
		return fmt.Errorf("invalid quota: %d", quota)
	}
	_, err := s.pool.Exec(ctx,
		`UPDATE accounts SET mailbox_quota = $2, updated_at = NOW() WHERE id = $1`,
		accountID, quota,
	)
	return err
}

// SetMailboxTTLMinutes 设置账户专属邮箱 TTL（NULL=默认，0=永不过期，正数=分钟）
func (s *Store) SetMailboxTTLMinutes(ctx context.Context, accountID uuid.UUID, ttlMinutes *int) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE accounts SET mailbox_ttl_minutes = $2, updated_at = NOW() WHERE id = $1`,
		accountID, ttlMinutes,
	)
	return err
}

// ExpireSVIPSweep 批量降级过期 SVIP 账户（后台定时任务可调用）
func (s *Store) ExpireSVIPSweep(ctx context.Context) (int64, error) {
	tag, err := s.pool.Exec(ctx,
		`UPDATE accounts SET svip_level = 0, updated_at = NOW()
		 WHERE svip_level > 0 AND svip_expires_at IS NOT NULL AND svip_expires_at <= NOW()`,
	)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// GetAdminAPIKey 获取第一个管理员账号的 API Key（用于写入 admin.key 文件）
func (s *Store) GetAdminAPIKey(ctx context.Context) (string, error) {
	var apiKey string
	err := s.pool.QueryRow(ctx,
		`SELECT api_key FROM accounts WHERE is_admin = TRUE ORDER BY created_at LIMIT 1`,
	).Scan(&apiKey)
	return apiKey, err
}

// ==================== Domain ====================

func (s *Store) AddDomain(ctx context.Context, domain string) (*model.Domain, error) {
	var d model.Domain
	err := s.pool.QueryRow(ctx,
		`INSERT INTO domains (domain, is_active, status) VALUES ($1, TRUE, 'active')
		 RETURNING id, domain, is_active, status, created_at, mx_checked_at`,
		strings.ToLower(domain),
	).Scan(&d.ID, &d.Domain, &d.IsActive, &d.Status, &d.CreatedAt, &d.MxCheckedAt)
	if err != nil {
		return nil, err
	}
	return &d, nil
}

// AddDomainPending 添加待验证域名（后台轮询 MX 记录）
func (s *Store) AddDomainPending(ctx context.Context, domain string) (*model.Domain, error) {
	var d model.Domain
	err := s.pool.QueryRow(ctx,
		`INSERT INTO domains (domain, is_active, status) VALUES ($1, FALSE, 'pending')
		 ON CONFLICT (domain) DO UPDATE
		   SET status = CASE WHEN domains.status = 'active' THEN 'active' ELSE 'pending' END,
		       is_active = CASE WHEN domains.status = 'active' THEN TRUE ELSE FALSE END
		 RETURNING id, domain, is_active, status, created_at, mx_checked_at`,
		strings.ToLower(domain),
	).Scan(&d.ID, &d.Domain, &d.IsActive, &d.Status, &d.CreatedAt, &d.MxCheckedAt)
	if err != nil {
		return nil, err
	}
	return &d, nil
}

func (s *Store) ListDomains(ctx context.Context) ([]model.Domain, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, domain, is_active, status, created_at, mx_checked_at FROM domains ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return pgx.CollectRows(rows, pgx.RowToStructByPos[model.Domain])
}

func (s *Store) GetActiveDomains(ctx context.Context) ([]model.Domain, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, domain, is_active, status, created_at, mx_checked_at FROM domains WHERE is_active = TRUE`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return pgx.CollectRows(rows, pgx.RowToStructByPos[model.Domain])
}

func (s *Store) GetRandomActiveDomain(ctx context.Context) (*model.Domain, error) {
	var d model.Domain
	err := s.pool.QueryRow(ctx,
		`SELECT id, domain, is_active, status, created_at, mx_checked_at FROM domains
		 WHERE is_active = TRUE ORDER BY RANDOM() LIMIT 1`,
	).Scan(&d.ID, &d.Domain, &d.IsActive, &d.Status, &d.CreatedAt, &d.MxCheckedAt)
	if err != nil {
		return nil, err
	}
	return &d, nil
}

// GetDomainByName 按域名字符串查找活跃域名，供创建邮箱时指定域名使用
func (s *Store) GetDomainByName(ctx context.Context, domain string) (*model.Domain, error) {
	var d model.Domain
	err := s.pool.QueryRow(ctx,
		`SELECT id, domain, is_active, status, created_at, mx_checked_at
		 FROM domains WHERE domain = $1 AND is_active = TRUE`,
		strings.ToLower(domain),
	).Scan(&d.ID, &d.Domain, &d.IsActive, &d.Status, &d.CreatedAt, &d.MxCheckedAt)
	if err != nil {
		return nil, err
	}
	return &d, nil
}

func (s *Store) GetDomainByID(ctx context.Context, domainID int) (*model.Domain, error) {
	var d model.Domain
	err := s.pool.QueryRow(ctx,
		`SELECT id, domain, is_active, status, created_at, mx_checked_at FROM domains WHERE id = $1`,
		domainID,
	).Scan(&d.ID, &d.Domain, &d.IsActive, &d.Status, &d.CreatedAt, &d.MxCheckedAt)
	if err != nil {
		return nil, err
	}
	return &d, nil
}

// ListPendingDomains 返回所有待验证域名
func (s *Store) ListPendingDomains(ctx context.Context) ([]model.Domain, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, domain, is_active, status, created_at, mx_checked_at
		 FROM domains WHERE status = 'pending'
		 ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return pgx.CollectRows(rows, pgx.RowToStructByPos[model.Domain])
}

// PromoteDomainToActive 验证通过，激活域名
func (s *Store) PromoteDomainToActive(ctx context.Context, domainID int) error {
	now := time.Now()
	_, err := s.pool.Exec(ctx,
		`UPDATE domains SET is_active = TRUE, status = 'active', mx_checked_at = $1 WHERE id = $2`,
		now, domainID)
	return err
}

// TouchDomainCheckTime 更新最后检测时间
func (s *Store) TouchDomainCheckTime(ctx context.Context, domainID int) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE domains SET mx_checked_at = NOW() WHERE id = $1`, domainID)
	return err
}

// DisableDomainMX MX检测失败，自动停用域名
func (s *Store) DisableDomainMX(ctx context.Context, domainID int) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE domains SET is_active = FALSE, status = 'disabled', mx_checked_at = NOW() WHERE id = $1`,
		domainID)
	return err
}

func (s *Store) DeleteDomain(ctx context.Context, domainID int) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM domains WHERE id = $1`, domainID)
	return err
}

func (s *Store) ToggleDomain(ctx context.Context, domainID int, active bool) error {
	status := "disabled"
	if active {
		status = "active"
	}
	_, err := s.pool.Exec(ctx,
		`UPDATE domains SET is_active = $1, status = $2 WHERE id = $3`, active, status, domainID)
	return err
}

// GetStats 返回全局统计数据
func (s *Store) GetStats(ctx context.Context) (*model.Stats, error) {
	var st model.Stats
	err := s.pool.QueryRow(ctx, `
		SELECT
		  (SELECT COUNT(*) FROM mailboxes)                         AS total_mailboxes,
		  (SELECT COUNT(*) FROM mailboxes WHERE expires_at IS NULL OR expires_at > NOW()) AS active_mailboxes,
		  (SELECT COUNT(*) FROM emails)                            AS total_emails,
		  (SELECT COUNT(*) FROM domains WHERE is_active = TRUE)    AS active_domains,
		  (SELECT COUNT(*) FROM domains WHERE status = 'pending')  AS pending_domains,
		  (SELECT COUNT(*) FROM accounts WHERE is_active = TRUE)   AS total_accounts
	`).Scan(
		&st.TotalMailboxes, &st.ActiveMailboxes,
		&st.TotalEmails, &st.ActiveDomains,
		&st.PendingDomains, &st.TotalAccounts,
	)
	if err != nil {
		return nil, err
	}
	return &st, nil
}

// ==================== Mailbox ====================

func (s *Store) CreateMailbox(ctx context.Context, accountID uuid.UUID, address string, domainID int, fullAddress string, ttlMinutes int) (*model.Mailbox, error) {
	var expiresAt *time.Time
	switch {
	case ttlMinutes > 0:
		t := time.Now().Add(time.Duration(ttlMinutes) * time.Minute)
		expiresAt = &t
	case ttlMinutes == 0:
		expiresAt = nil
	default:
		t := time.Now().Add(30 * time.Minute)
		expiresAt = &t
	}
	var m model.Mailbox
	err := s.pool.QueryRow(ctx,
		`INSERT INTO mailboxes (account_id, address, domain_id, full_address, expires_at)
		 VALUES ($1, $2, $3, $4, $5)
		 RETURNING id, account_id, address, domain_id, full_address, created_at, expires_at`,
		accountID, address, domainID, fullAddress, expiresAt,
	).Scan(&m.ID, &m.AccountID, &m.Address, &m.DomainID, &m.FullAddress, &m.CreatedAt, &m.ExpiresAt)
	if err != nil {
		return nil, err
	}
	return &m, nil
}

func (s *Store) ListMailboxes(ctx context.Context, accountID uuid.UUID, page, size int) ([]model.Mailbox, int, error) {
	var total int
	err := s.pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM mailboxes WHERE account_id = $1`, accountID).Scan(&total)
	if err != nil {
		return nil, 0, err
	}

	rows, err := s.pool.Query(ctx,
		`SELECT id, account_id, address, domain_id, full_address, created_at, expires_at
		 FROM mailboxes WHERE account_id = $1
		 ORDER BY created_at DESC LIMIT $2 OFFSET $3`,
		accountID, size, (page-1)*size,
	)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	mailboxes, err := pgx.CollectRows(rows, pgx.RowToStructByPos[model.Mailbox])
	if err != nil {
		return nil, 0, err
	}
	return mailboxes, total, nil
}

func (s *Store) GetMailbox(ctx context.Context, mailboxID uuid.UUID, accountID uuid.UUID) (*model.Mailbox, error) {
	var m model.Mailbox
	err := s.pool.QueryRow(ctx,
		`SELECT id, account_id, address, domain_id, full_address, created_at, expires_at
		 FROM mailboxes WHERE id = $1 AND account_id = $2`,
		mailboxID, accountID,
	).Scan(&m.ID, &m.AccountID, &m.Address, &m.DomainID, &m.FullAddress, &m.CreatedAt, &m.ExpiresAt)
	if err != nil {
		return nil, err
	}
	return &m, nil
}

func (s *Store) DeleteMailbox(ctx context.Context, mailboxID uuid.UUID, accountID uuid.UUID) error {
	tag, err := s.pool.Exec(ctx,
		`DELETE FROM mailboxes WHERE id = $1 AND account_id = $2`, mailboxID, accountID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}

func (s *Store) GetMailboxByFullAddress(ctx context.Context, fullAddress string) (*model.Mailbox, error) {
	var m model.Mailbox
	err := s.pool.QueryRow(ctx,
		`SELECT id, account_id, address, domain_id, full_address, created_at, expires_at
		 FROM mailboxes WHERE full_address = $1`,
		strings.ToLower(fullAddress),
	).Scan(&m.ID, &m.AccountID, &m.Address, &m.DomainID, &m.FullAddress, &m.CreatedAt, &m.ExpiresAt)
	if err != nil {
		return nil, err
	}
	return &m, nil
}

// DeleteExpiredMailboxes 刪除已过期的邮箱（及其所有邮件）
func (s *Store) DeleteExpiredMailboxes(ctx context.Context) (int64, error) {
	tag, err := s.pool.Exec(ctx, `DELETE FROM mailboxes WHERE expires_at IS NOT NULL AND expires_at < NOW()`)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// CheckDomainMX 检测域名MX记录是否指向指定服务器IP
func CheckDomainMX(domain, serverIP string) (matched bool, mxHosts []string, status string) {
	mxRecords, err := net.LookupMX(domain)
	if err != nil {
		return false, nil, fmt.Sprintf("DNS查询失败: %v", err)
	}
	if len(mxRecords) == 0 {
		return false, nil, "未找到MX记录，请先配置MX记录"
	}
	for _, mx := range mxRecords {
		host := strings.TrimSuffix(mx.Host, ".")
		mxHosts = append(mxHosts, host)
		addrs, err := net.LookupHost(host)
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			if addr == serverIP {
				return true, mxHosts, fmt.Sprintf("✓ MX记录匹配：%s → %s", host, addr)
			}
		}
	}
	return false, mxHosts, fmt.Sprintf("MX记录(%s)未指向本服务器(%s)", strings.Join(mxHosts, ","), serverIP)
}

// ==================== Email ====================

func (s *Store) InsertEmail(ctx context.Context, mailboxID uuid.UUID, sender, subject, bodyText, bodyHTML, raw string) (*model.Email, error) {
	var e model.Email
	err := s.pool.QueryRow(ctx,
		`INSERT INTO emails (mailbox_id, sender, subject, body_text, body_html, raw_message, size_bytes)
		 VALUES ($1, $2, $3, $4, $5, $6, $7)
		 RETURNING id, mailbox_id, sender, subject, body_text, body_html, raw_message, size_bytes, received_at`,
		mailboxID, sender, subject, bodyText, bodyHTML, raw, len(raw),
	).Scan(&e.ID, &e.MailboxID, &e.Sender, &e.Subject, &e.BodyText, &e.BodyHTML, &e.RawMessage, &e.SizeBytes, &e.ReceivedAt)
	if err != nil {
		return nil, err
	}
	return &e, nil
}

func (s *Store) ListEmails(ctx context.Context, mailboxID uuid.UUID, page, size int) ([]model.EmailSummary, int, error) {
	var total int
	err := s.pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM emails WHERE mailbox_id = $1`, mailboxID).Scan(&total)
	if err != nil {
		return nil, 0, err
	}

	rows, err := s.pool.Query(ctx,
		`SELECT id, sender, subject, size_bytes, received_at
		 FROM emails WHERE mailbox_id = $1
		 ORDER BY received_at DESC LIMIT $2 OFFSET $3`,
		mailboxID, size, (page-1)*size,
	)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	emails, err := pgx.CollectRows(rows, pgx.RowToStructByPos[model.EmailSummary])
	if err != nil {
		return nil, 0, err
	}
	return emails, total, nil
}

func (s *Store) GetEmail(ctx context.Context, emailID uuid.UUID, mailboxID uuid.UUID) (*model.Email, error) {
	var e model.Email
	err := s.pool.QueryRow(ctx,
		`SELECT id, mailbox_id, sender, subject, body_text, body_html, raw_message, size_bytes, received_at
		 FROM emails WHERE id = $1 AND mailbox_id = $2`,
		emailID, mailboxID,
	).Scan(&e.ID, &e.MailboxID, &e.Sender, &e.Subject, &e.BodyText, &e.BodyHTML, &e.RawMessage, &e.SizeBytes, &e.ReceivedAt)
	if err != nil {
		return nil, err
	}
	return &e, nil
}

func (s *Store) DeleteEmail(ctx context.Context, emailID uuid.UUID, mailboxID uuid.UUID) error {
	tag, err := s.pool.Exec(ctx,
		`DELETE FROM emails WHERE id = $1 AND mailbox_id = $2`, emailID, mailboxID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}

// ==================== Helpers ====================

func generateAPIKey() string {
	b := make([]byte, 24)
	rand.Read(b)
	return "tm_" + hex.EncodeToString(b)
}

func GenerateRandomAddress() string {
	const chars = "abcdefghijklmnopqrstuvwxyz0123456789"
	length := 10
	result := make([]byte, length)
	for i := range result {
		n, _ := rand.Int(rand.Reader, big.NewInt(int64(len(chars))))
		result[i] = chars[n.Int64()]
	}
	return string(result)
}
