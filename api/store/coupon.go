package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"tempmail/model"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ==================== v10：优惠券 ====================

var (
	// ErrCouponNotFound 优惠券不存在或已禁用
	ErrCouponNotFound = errors.New("coupon not found")
	// ErrCouponExpired 券已过期或未开始
	ErrCouponExpired = errors.New("coupon expired")
	// ErrCouponQuotaExhausted 总发放数达到上限
	ErrCouponQuotaExhausted = errors.New("coupon quota exhausted")
	// ErrCouponPerUserExceeded 单用户已达到领取次数上限
	ErrCouponPerUserExceeded = errors.New("per-user limit exceeded")
	// ErrCouponSVIPOnly 仅 SVIP 可领
	ErrCouponSVIPOnly = errors.New("coupon is SVIP only")
	// ErrCouponOwnedAvailable 已持有未使用的同款券
	ErrCouponOwnedAvailable = errors.New("you already hold an unused copy of this coupon")
)

const couponColumns = `id, code, name, description, discount_type, discount_value,
	min_order_cents, max_discount_cents, total_quota, used_count, per_user_limit,
	starts_at, expires_at, svip_only, new_user_gift, svip_gift, enabled, created_at, updated_at`

const userCouponColumns = `id, account_id, coupon_id, status, order_id, acquired_at, used_at,
	snapshot_name, snapshot_discount_type, snapshot_discount_value,
	snapshot_min_order_cents, snapshot_max_discount_cents, snapshot_expires_at`

// scanCoupon 通用 Coupon Scan
func scanCoupon(row interface {
	Scan(dest ...interface{}) error
}) (*model.Coupon, error) {
	var c model.Coupon
	var code sql.NullString
	if err := row.Scan(
		&c.ID, &code, &c.Name, &c.Description, &c.DiscountType, &c.DiscountValue,
		&c.MinOrderCents, &c.MaxDiscountCents, &c.TotalQuota, &c.UsedCount, &c.PerUserLimit,
		&c.StartsAt, &c.ExpiresAt, &c.SVIPOnly, &c.NewUserGift, &c.SVIPGift, &c.Enabled,
		&c.CreatedAt, &c.UpdatedAt,
	); err != nil {
		return nil, err
	}
	if code.Valid {
		v := code.String
		c.Code = &v
	}
	return &c, nil
}

// scanUserCoupon 通用 UserCoupon Scan
func scanUserCoupon(row interface {
	Scan(dest ...interface{}) error
}) (*model.UserCoupon, error) {
	var uc model.UserCoupon
	if err := row.Scan(
		&uc.ID, &uc.AccountID, &uc.CouponID, &uc.Status, &uc.OrderID, &uc.AcquiredAt, &uc.UsedAt,
		&uc.SnapshotName, &uc.SnapshotDiscountType, &uc.SnapshotDiscountValue,
		&uc.SnapshotMinOrderCents, &uc.SnapshotMaxDiscountCents, &uc.SnapshotExpiresAt,
	); err != nil {
		return nil, err
	}
	return &uc, nil
}

// ==================== 管理员 CRUD ====================

// CouponListFilter 管理员列表筛选
type CouponListFilter struct {
	Search string // name / code / description 模糊
	Status string // all|enabled|disabled|expired
}

func (s *Store) ListCoupons(ctx context.Context, page, size int, filter CouponListFilter) ([]model.Coupon, int, error) {
	conds := []string{}
	args := []interface{}{}
	switch strings.ToLower(strings.TrimSpace(filter.Status)) {
	case "enabled":
		conds = append(conds, "enabled = TRUE AND (expires_at IS NULL OR expires_at > NOW())")
	case "disabled":
		conds = append(conds, "enabled = FALSE")
	case "expired":
		conds = append(conds, "expires_at IS NOT NULL AND expires_at <= NOW()")
	}
	q := strings.TrimSpace(filter.Search)
	if q != "" {
		args = append(args, "%"+q+"%")
		conds = append(conds, fmt.Sprintf("(name ILIKE $%d OR description ILIKE $%d OR code ILIKE $%d)",
			len(args), len(args), len(args)))
	}
	where := ""
	if len(conds) > 0 {
		where = " WHERE " + strings.Join(conds, " AND ")
	}

	var total int
	if err := s.pool.QueryRow(ctx, `SELECT COUNT(*) FROM coupons`+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	limitArgs := append([]interface{}{}, args...)
	limitArgs = append(limitArgs, size, (page-1)*size)
	sql := `SELECT ` + couponColumns + ` FROM coupons` + where +
		fmt.Sprintf(` ORDER BY created_at DESC LIMIT $%d OFFSET $%d`, len(limitArgs)-1, len(limitArgs))

	rows, err := s.pool.Query(ctx, sql, limitArgs...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	out := []model.Coupon{}
	for rows.Next() {
		c, err := scanCoupon(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, *c)
	}
	return out, total, rows.Err()
}

func (s *Store) GetCouponByID(ctx context.Context, id uuid.UUID) (*model.Coupon, error) {
	row := s.pool.QueryRow(ctx, `SELECT `+couponColumns+` FROM coupons WHERE id = $1`, id)
	return scanCoupon(row)
}

func (s *Store) GetCouponByCode(ctx context.Context, code string) (*model.Coupon, error) {
	row := s.pool.QueryRow(ctx, `SELECT `+couponColumns+` FROM coupons WHERE code = $1 AND enabled = TRUE`, code)
	c, err := scanCoupon(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrCouponNotFound
		}
		return nil, err
	}
	return c, nil
}

// CreateCoupon 管理员创建券
func (s *Store) CreateCoupon(ctx context.Context, c *model.Coupon) (*model.Coupon, error) {
	if c.DiscountType != "percentage" && c.DiscountType != "fixed" {
		return nil, fmt.Errorf("invalid discount_type: %s", c.DiscountType)
	}
	if c.DiscountType == "percentage" && (c.DiscountValue <= 0 || c.DiscountValue > 100) {
		return nil, fmt.Errorf("percentage must be 1-100")
	}
	if c.DiscountValue <= 0 {
		return nil, fmt.Errorf("discount_value must be > 0")
	}
	if c.PerUserLimit <= 0 {
		c.PerUserLimit = 1
	}
	var code interface{}
	if c.Code != nil {
		trimmed := strings.TrimSpace(*c.Code)
		if trimmed != "" {
			code = trimmed
		}
	}

	row := s.pool.QueryRow(ctx, `
		INSERT INTO coupons (
			code, name, description, discount_type, discount_value,
			min_order_cents, max_discount_cents, total_quota, per_user_limit,
			starts_at, expires_at, svip_only, new_user_gift, svip_gift, enabled
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)
		RETURNING `+couponColumns,
		code, c.Name, c.Description, c.DiscountType, c.DiscountValue,
		c.MinOrderCents, c.MaxDiscountCents, c.TotalQuota, c.PerUserLimit,
		c.StartsAt, c.ExpiresAt, c.SVIPOnly, c.NewUserGift, c.SVIPGift, c.Enabled,
	)
	return scanCoupon(row)
}

// UpdateCoupon 管理员更新（全量覆盖）
func (s *Store) UpdateCoupon(ctx context.Context, c *model.Coupon) (*model.Coupon, error) {
	var code interface{}
	if c.Code != nil {
		trimmed := strings.TrimSpace(*c.Code)
		if trimmed != "" {
			code = trimmed
		}
	}
	row := s.pool.QueryRow(ctx, `
		UPDATE coupons SET
			code = $2, name = $3, description = $4, discount_type = $5, discount_value = $6,
			min_order_cents = $7, max_discount_cents = $8, total_quota = $9, per_user_limit = $10,
			starts_at = $11, expires_at = $12, svip_only = $13, new_user_gift = $14,
			svip_gift = $15, enabled = $16, updated_at = NOW()
		WHERE id = $1
		RETURNING `+couponColumns,
		c.ID, code, c.Name, c.Description, c.DiscountType, c.DiscountValue,
		c.MinOrderCents, c.MaxDiscountCents, c.TotalQuota, c.PerUserLimit,
		c.StartsAt, c.ExpiresAt, c.SVIPOnly, c.NewUserGift, c.SVIPGift, c.Enabled,
	)
	return scanCoupon(row)
}

// SetCouponEnabled 启用/停用
func (s *Store) SetCouponEnabled(ctx context.Context, id uuid.UUID, enabled bool) error {
	tag, err := s.pool.Exec(ctx,
		`UPDATE coupons SET enabled = $2, updated_at = NOW() WHERE id = $1`,
		id, enabled,
	)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}

// DeleteCoupon 硬删（级联 CASCADE 清理已领取记录）
func (s *Store) DeleteCoupon(ctx context.Context, id uuid.UUID) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM coupons WHERE id = $1`, id)
	return err
}

// ==================== 领取 / 使用 ====================

// couponEligible 判断券是否处于可领取/可使用的全局状态（不涉及用户个体限制）
func couponEligible(c *model.Coupon, now time.Time) error {
	if !c.Enabled {
		return ErrCouponNotFound
	}
	if c.StartsAt != nil && c.StartsAt.After(now) {
		return ErrCouponExpired
	}
	if c.ExpiresAt != nil && c.ExpiresAt.Before(now) {
		return ErrCouponExpired
	}
	if c.TotalQuota > 0 && c.UsedCount >= c.TotalQuota {
		return ErrCouponQuotaExhausted
	}
	return nil
}

// RedeemCoupon 用户用 code 领取（并发安全）
//
//	accountSVIP = 当前账户是否 SVIP（由 handler 层根据 account.IsSVIP() 判断）
//	成功返回领取的 user_coupon 记录
func (s *Store) RedeemCoupon(ctx context.Context, accountID uuid.UUID, code string, accountSVIP bool) (*model.UserCoupon, error) {
	code = strings.TrimSpace(code)
	if code == "" {
		return nil, ErrCouponNotFound
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	var c model.Coupon
	var nc sql.NullString
	err = tx.QueryRow(ctx, `SELECT `+couponColumns+` FROM coupons WHERE code = $1 FOR UPDATE`, code).Scan(
		&c.ID, &nc, &c.Name, &c.Description, &c.DiscountType, &c.DiscountValue,
		&c.MinOrderCents, &c.MaxDiscountCents, &c.TotalQuota, &c.UsedCount, &c.PerUserLimit,
		&c.StartsAt, &c.ExpiresAt, &c.SVIPOnly, &c.NewUserGift, &c.SVIPGift, &c.Enabled,
		&c.CreatedAt, &c.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrCouponNotFound
		}
		return nil, err
	}
	if nc.Valid {
		v := nc.String
		c.Code = &v
	}

	if err := couponEligible(&c, time.Now()); err != nil {
		return nil, err
	}
	if c.SVIPOnly && !accountSVIP {
		return nil, ErrCouponSVIPOnly
	}

	// 单用户领取次数上限
	var cnt int
	if err := tx.QueryRow(ctx,
		`SELECT COUNT(*) FROM user_coupons WHERE account_id = $1 AND coupon_id = $2`,
		accountID, c.ID,
	).Scan(&cnt); err != nil {
		return nil, err
	}
	if cnt >= c.PerUserLimit {
		return nil, ErrCouponPerUserExceeded
	}
	// 同一用户最多持有 1 张未使用（数据库约束会兜底）
	var avail int
	if err := tx.QueryRow(ctx,
		`SELECT COUNT(*) FROM user_coupons WHERE account_id = $1 AND coupon_id = $2 AND status = 'available'`,
		accountID, c.ID,
	).Scan(&avail); err != nil {
		return nil, err
	}
	if avail > 0 {
		return nil, ErrCouponOwnedAvailable
	}

	// quota 原子递增
	if _, err := tx.Exec(ctx,
		`UPDATE coupons SET used_count = used_count + 1, updated_at = NOW() WHERE id = $1`, c.ID,
	); err != nil {
		return nil, err
	}

	uc, err := insertUserCouponTx(ctx, tx, accountID, &c)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return uc, nil
}

// GrantCouponDirect 管理员定向派发
//
//	不校验 svip_only / per_user_limit（管理员有特权），但仍会把快照字段写入。
//	如果该账户已持有一张未使用的同款券，会报 ErrCouponOwnedAvailable。
func (s *Store) GrantCouponDirect(ctx context.Context, accountID uuid.UUID, couponID uuid.UUID) (*model.UserCoupon, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	var c model.Coupon
	var nc sql.NullString
	err = tx.QueryRow(ctx, `SELECT `+couponColumns+` FROM coupons WHERE id = $1 FOR UPDATE`, couponID).Scan(
		&c.ID, &nc, &c.Name, &c.Description, &c.DiscountType, &c.DiscountValue,
		&c.MinOrderCents, &c.MaxDiscountCents, &c.TotalQuota, &c.UsedCount, &c.PerUserLimit,
		&c.StartsAt, &c.ExpiresAt, &c.SVIPOnly, &c.NewUserGift, &c.SVIPGift, &c.Enabled,
		&c.CreatedAt, &c.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrCouponNotFound
		}
		return nil, err
	}
	if nc.Valid {
		v := nc.String
		c.Code = &v
	}
	if !c.Enabled {
		return nil, ErrCouponNotFound
	}

	var avail int
	if err := tx.QueryRow(ctx,
		`SELECT COUNT(*) FROM user_coupons WHERE account_id = $1 AND coupon_id = $2 AND status = 'available'`,
		accountID, c.ID,
	).Scan(&avail); err != nil {
		return nil, err
	}
	if avail > 0 {
		return nil, ErrCouponOwnedAvailable
	}

	if c.TotalQuota > 0 && c.UsedCount >= c.TotalQuota {
		return nil, ErrCouponQuotaExhausted
	}
	if _, err := tx.Exec(ctx,
		`UPDATE coupons SET used_count = used_count + 1, updated_at = NOW() WHERE id = $1`, c.ID,
	); err != nil {
		return nil, err
	}

	uc, err := insertUserCouponTx(ctx, tx, accountID, &c)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return uc, nil
}

// insertUserCouponTx 内部工具：写入 user_coupons 快照
func insertUserCouponTx(ctx context.Context, tx pgx.Tx, accountID uuid.UUID, c *model.Coupon) (*model.UserCoupon, error) {
	row := tx.QueryRow(ctx, `
		INSERT INTO user_coupons (
			account_id, coupon_id, status,
			snapshot_name, snapshot_discount_type, snapshot_discount_value,
			snapshot_min_order_cents, snapshot_max_discount_cents, snapshot_expires_at
		) VALUES ($1,$2,'available',$3,$4,$5,$6,$7,$8)
		RETURNING `+userCouponColumns,
		accountID, c.ID, c.Name, c.DiscountType, c.DiscountValue,
		c.MinOrderCents, c.MaxDiscountCents, c.ExpiresAt,
	)
	return scanUserCoupon(row)
}

// ListMyCoupons 用户查询自己持有的券（按状态分类）
//
//	status: "" | "all" 全部；"available" 可用；"used" 已用；"expired" 已过期
func (s *Store) ListMyCoupons(ctx context.Context, accountID uuid.UUID, status string) ([]model.UserCoupon, error) {
	cond := ""
	args := []interface{}{accountID}
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "available":
		cond = " AND status = 'available' AND (snapshot_expires_at IS NULL OR snapshot_expires_at > NOW())"
	case "used":
		cond = " AND status = 'used'"
	case "expired":
		cond = " AND (status = 'expired' OR (status = 'available' AND snapshot_expires_at IS NOT NULL AND snapshot_expires_at <= NOW()))"
	}
	rows, err := s.pool.Query(ctx,
		`SELECT `+userCouponColumns+` FROM user_coupons WHERE account_id = $1`+cond+
			` ORDER BY status = 'available' DESC, acquired_at DESC`,
		args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []model.UserCoupon{}
	for rows.Next() {
		uc, err := scanUserCoupon(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *uc)
	}
	return out, rows.Err()
}

// GetUserCoupon 查询某张具体的用户券
func (s *Store) GetUserCoupon(ctx context.Context, accountID, userCouponID uuid.UUID) (*model.UserCoupon, error) {
	row := s.pool.QueryRow(ctx,
		`SELECT `+userCouponColumns+` FROM user_coupons WHERE id = $1 AND account_id = $2`,
		userCouponID, accountID,
	)
	return scanUserCoupon(row)
}

// LockUserCouponForOrder 下单时锁定一张用户券（标记为待使用，但不立即置 used；支付成功才扣减）
//
//	实现：直接检查并保持 available，但把 order_id 写入作为"预占用"。
//	若支付失败/订单取消：UnlockUserCoupon 清回 available + order_id=NULL。
//	若发货成功：MarkUserCouponUsed 置 used + 写 used_at。
func (s *Store) LockUserCouponForOrder(ctx context.Context, accountID, userCouponID, orderID uuid.UUID) (*model.UserCoupon, error) {
	tag, err := s.pool.Exec(ctx,
		`UPDATE user_coupons SET order_id = $3
		 WHERE id = $1 AND account_id = $2 AND status = 'available' AND (order_id IS NULL OR order_id = $3)
		   AND (snapshot_expires_at IS NULL OR snapshot_expires_at > NOW())`,
		userCouponID, accountID, orderID,
	)
	if err != nil {
		return nil, err
	}
	if tag.RowsAffected() == 0 {
		return nil, fmt.Errorf("coupon not available or already used")
	}
	return s.GetUserCoupon(ctx, accountID, userCouponID)
}

// UnlockUserCoupon 订单取消时释放券
func (s *Store) UnlockUserCoupon(ctx context.Context, orderID uuid.UUID) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE user_coupons SET order_id = NULL WHERE order_id = $1 AND status = 'available'`,
		orderID,
	)
	return err
}

// MarkUserCouponUsed 发货成功后置为已用
func (s *Store) MarkUserCouponUsed(ctx context.Context, orderID uuid.UUID) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE user_coupons SET status = 'used', used_at = NOW() WHERE order_id = $1 AND status = 'available'`,
		orderID,
	)
	return err
}

// ExpireUserCoupons 定时任务：把 snapshot_expires_at 已过期的 available 券置为 expired
func (s *Store) ExpireUserCoupons(ctx context.Context) (int64, error) {
	tag, err := s.pool.Exec(ctx,
		`UPDATE user_coupons SET status = 'expired'
		 WHERE status = 'available' AND snapshot_expires_at IS NOT NULL AND snapshot_expires_at <= NOW()`,
	)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// ==================== 自动发放（新用户 / SVIP）====================

// GrantAutoGifts 批量把符合标志位的模板发给指定用户。忽略已持有者等错误，尽可能多地发。
//
//	flag: "new_user" | "svip"
//	返回成功发出的数量
func (s *Store) GrantAutoGifts(ctx context.Context, accountID uuid.UUID, flag string) (int, error) {
	var col string
	switch flag {
	case "new_user":
		col = "new_user_gift"
	case "svip":
		col = "svip_gift"
	default:
		return 0, fmt.Errorf("invalid flag: %s", flag)
	}
	rows, err := s.pool.Query(ctx,
		`SELECT id FROM coupons WHERE `+col+` = TRUE AND enabled = TRUE
		   AND (starts_at IS NULL OR starts_at <= NOW())
		   AND (expires_at IS NULL OR expires_at > NOW())
		   AND (total_quota = 0 OR used_count < total_quota)`,
	)
	if err != nil {
		return 0, err
	}
	ids := []uuid.UUID{}
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return 0, err
		}
		ids = append(ids, id)
	}
	rows.Close()

	granted := 0
	for _, id := range ids {
		if _, err := s.GrantCouponDirect(ctx, accountID, id); err == nil {
			granted++
		}
	}
	return granted, nil
}

// ==================== 折扣计算 ====================

// QuoteDiscount 计算给定订单原价下某张券的折扣结果（用于用户下单前预览）
//
//	返回的 DiscountQuote 里 CouponCode 会尽力填充（code NULL 则为空）。
func (s *Store) QuoteDiscount(ctx context.Context, accountID, userCouponID uuid.UUID, originalCents int) (*model.DiscountQuote, error) {
	uc, err := s.GetUserCoupon(ctx, accountID, userCouponID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("coupon not found for this user")
		}
		return nil, err
	}
	if uc.Status != "available" {
		return nil, fmt.Errorf("coupon status not usable: %s", uc.Status)
	}
	if uc.SnapshotExpiresAt != nil && uc.SnapshotExpiresAt.Before(time.Now()) {
		return nil, fmt.Errorf("coupon expired")
	}
	if originalCents < uc.SnapshotMinOrderCents {
		return nil, fmt.Errorf("订单金额未达到 %d 分使用门槛", uc.SnapshotMinOrderCents)
	}

	discount := computeDiscount(uc.SnapshotDiscountType, uc.SnapshotDiscountValue,
		uc.SnapshotMaxDiscountCents, originalCents)

	quote := &model.DiscountQuote{
		OriginalCents: originalCents,
		DiscountCents: discount,
		PayableCents:  originalCents - discount,
		CouponName:    uc.SnapshotName,
	}
	// 再去模板里捞 code（可能已被管理员删除，忽略错误）
	if c, err := s.GetCouponByID(ctx, uc.CouponID); err == nil && c != nil && c.Code != nil {
		quote.CouponCode = *c.Code
	}
	return quote, nil
}

// computeDiscount 纯函数折扣计算
func computeDiscount(discountType string, discountValue, maxDiscountCents, originalCents int) int {
	if originalCents <= 0 {
		return 0
	}
	var d int
	switch discountType {
	case "percentage":
		d = originalCents * discountValue / 100
		if maxDiscountCents > 0 && d > maxDiscountCents {
			d = maxDiscountCents
		}
	case "fixed":
		d = discountValue
	default:
		return 0
	}
	if d > originalCents {
		d = originalCents
	}
	if d < 0 {
		d = 0
	}
	return d
}

// ComputeDiscountFromSnapshot 外部可用的折扣计算（订单下单时用）
func ComputeDiscountFromSnapshot(uc *model.UserCoupon, originalCents int) int {
	if uc == nil {
		return 0
	}
	return computeDiscount(uc.SnapshotDiscountType, uc.SnapshotDiscountValue,
		uc.SnapshotMaxDiscountCents, originalCents)
}
