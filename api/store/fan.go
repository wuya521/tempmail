package store

import (
	"context"
	"errors"
	"strconv"
	"strings"

	"tempmail/model"

	"github.com/google/uuid"
)

// ErrExclusiveFanNotEligible 表示当前购买次数尚未达到"专属老粉"领取门槛。
var ErrExclusiveFanNotEligible = errors.New("exclusive fan not eligible")

// ExclusiveFanConfig "专属老粉"认证配置。
//
// DiscountBps 使用基点表示商品实付比例：
//   - 10000 = 10 折（无折扣）
//   - 9500  = 9.5 折
//   - 8800  = 8.8 折
type ExclusiveFanConfig struct {
	Enabled     bool `json:"enabled"`
	MinOrders   int  `json:"min_orders"`
	DiscountBps int  `json:"discount_bps"`
}

func parseBoolSetting(v string, def bool) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "true", "1", "yes", "on":
		return true
	case "false", "0", "no", "off":
		return false
	default:
		return def
	}
}

func parseIntSetting(v string, def int) int {
	n, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil {
		return def
	}
	return n
}

// GetExclusiveFanConfig 读取专属老粉配置；缺省值偏温和：
// 开启、满 3 笔已发货订单可领、9.5 折。
func (s *Store) GetExclusiveFanConfig(ctx context.Context) ExclusiveFanConfig {
	cfg := ExclusiveFanConfig{
		Enabled:     true,
		MinOrders:   3,
		DiscountBps: 9500,
	}
	if v, err := s.GetSetting(ctx, "exclusive_fan_enabled"); err == nil {
		cfg.Enabled = parseBoolSetting(v, cfg.Enabled)
	}
	if v, err := s.GetSetting(ctx, "exclusive_fan_min_orders"); err == nil {
		cfg.MinOrders = parseIntSetting(v, cfg.MinOrders)
	}
	if cfg.MinOrders < 1 {
		cfg.MinOrders = 1
	}
	if v, err := s.GetSetting(ctx, "exclusive_fan_discount_bps"); err == nil {
		cfg.DiscountBps = parseIntSetting(v, cfg.DiscountBps)
	}
	// 允许 1 折到 10 折；异常值回落到无折扣，避免误伤价格。
	if cfg.DiscountBps < 1000 || cfg.DiscountBps > 10000 {
		cfg.DiscountBps = 10000
	}
	return cfg
}

// CountFulfilledClaudeOrdersForAccount 返回某账户已发货订单次数（按订单笔数，不按件数）。
func (s *Store) CountFulfilledClaudeOrdersForAccount(ctx context.Context, accountID uuid.UUID) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx,
		`SELECT COUNT(*)::int FROM claude_orders WHERE account_id = $1 AND status = 'fulfilled'`,
		accountID,
	).Scan(&n)
	return n, err
}

// AdminGrantExclusiveFan 管理员直接赠送老粉认证（无需满足订单门槛）
func (s *Store) AdminGrantExclusiveFan(ctx context.Context, accountID uuid.UUID, level int) (*model.Account, bool, error) {
	if level <= 0 {
		level = 1
	}
	acc, err := s.GetAccountByID(ctx, accountID)
	if err != nil {
		return nil, false, err
	}
	if acc.ExclusiveFanLevel >= level {
		return acc, false, nil
	}
	_, err = s.pool.Exec(ctx,
		`UPDATE accounts
		 SET exclusive_fan_level = $2,
		     exclusive_fan_claimed_at = COALESCE(exclusive_fan_claimed_at, NOW()),
		     updated_at = NOW()
		 WHERE id = $1`,
		accountID, level,
	)
	if err != nil {
		return nil, false, err
	}
	acc, err = s.GetAccountByID(ctx, accountID)
	if err != nil {
		return nil, false, err
	}
	return acc, true, nil
}

// AdminRevokeExclusiveFan 管理员撤销老粉认证
func (s *Store) AdminRevokeExclusiveFan(ctx context.Context, accountID uuid.UUID) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE accounts SET exclusive_fan_level = 0, exclusive_fan_claimed_at = NULL, updated_at = NOW() WHERE id = $1`,
		accountID,
	)
	return err
}

// ClaimExclusiveFan 在满足门槛时为用户领取"专属老粉"认证。
// 返回：最新账号、是否本次新领取、已完成订单笔数。
func (s *Store) ClaimExclusiveFan(ctx context.Context, accountID uuid.UUID, minOrders int) (*model.Account, bool, int, error) {
	if minOrders < 1 {
		minOrders = 1
	}
	doneOrders, err := s.CountFulfilledClaudeOrdersForAccount(ctx, accountID)
	if err != nil {
		return nil, false, 0, err
	}
	if doneOrders < minOrders {
		return nil, false, doneOrders, ErrExclusiveFanNotEligible
	}

	acc, err := s.GetAccountByID(ctx, accountID)
	if err != nil {
		return nil, false, doneOrders, err
	}
	if acc.IsExclusiveFan() {
		return acc, false, doneOrders, nil
	}

	_, err = s.pool.Exec(ctx,
		`UPDATE accounts
		 SET exclusive_fan_level = 1,
		     exclusive_fan_claimed_at = COALESCE(exclusive_fan_claimed_at, NOW()),
		     updated_at = NOW()
		 WHERE id = $1`,
		accountID,
	)
	if err != nil {
		return nil, false, doneOrders, err
	}
	acc, err = s.GetAccountByID(ctx, accountID)
	if err != nil {
		return nil, false, doneOrders, err
	}
	return acc, true, doneOrders, nil
}
