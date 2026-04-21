package store

import (
	"context"
	"database/sql"
	"encoding/csv"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"tempmail/model"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

var inventoryEmailRe = regexp.MustCompile(`^[^\s@]+@[^\s@]+\.[^\s@]+$`)

// ClaudeShopConfig 店铺配置（单行 id=1）
type ClaudeShopConfig struct {
	Enabled             bool
	Title               string
	Subtitle            string
	Description         string
	TutorialURL         string
	RetailPriceCents    int
	WholesaleMinQty     int
	WholesalePriceCents int
	TagHot              bool
	ShowTagWholesale    bool
	TagFanWelfare       string
	MaxPerUser          int
	WechatQRFile        string
	AlipayQRFile        string
	// StaticPaymentManualConfirm 为 false 时，静态收款路径下单后立即尝试自动发货（无支付凭证，请谨慎）
	StaticPaymentManualConfirm bool
	// StaticQREnabled 为 false 时：不提供静态码支付，且不限制「一单待支付」；为 true 时恢复静态码与待确认期间禁止新单
	StaticQREnabled bool
}

var inventorySplitTokens = []string{"####", "----", "===="}
var inventoryHeaderEmailTokens = []string{"邮箱账号", "邮箱", "邮箱地址", "email", "mail", "mailbox", "account"}
var inventoryHeaderKeyTokens = []string{"邮箱apikey", "apikey", "api_key", "api key", "key", "登录key", "登录密钥", "密钥", "token"}

// InventoryPair 导入的一行库存
type InventoryPair struct {
	Email  string
	APIKey string
}

// ParseInventoryImport 解析 .txt（####/----/====）或单行 CSV（两列，自动识别邮箱列）
func ParseInventoryImport(raw string) ([]InventoryPair, []string) {
	var pairs []InventoryPair
	var warnings []string
	seen := make(map[string]struct{})
	text := strings.TrimPrefix(strings.ReplaceAll(raw, "\r\n", "\n"), "\ufeff")
	lines := strings.Split(text, "\n")
	for i, rawLine := range lines {
		lineNo := i + 1
		line := strings.TrimSpace(rawLine)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "#") {
			continue
		}
		if isInventoryHeaderLine(line) {
			continue
		}
		email, key, ok := splitInventoryLine(line)
		if !ok {
			warnings = append(warnings, fmt.Sprintf("第 %d 行：无法识别格式", lineNo))
			continue
		}
		if email == "" || key == "" {
			warnings = append(warnings, fmt.Sprintf("第 %d 行：邮箱或 Key 为空", lineNo))
			continue
		}
		if len(email) > 320 {
			warnings = append(warnings, fmt.Sprintf("第 %d 行：邮箱长度超过 320", lineNo))
			continue
		}
		if len(key) > 128 {
			warnings = append(warnings, fmt.Sprintf("第 %d 行：Key 长度超过 128", lineNo))
			continue
		}
		dedupeKey := strings.ToLower(email) + "\x00" + key
		if _, ok := seen[dedupeKey]; ok {
			warnings = append(warnings, fmt.Sprintf("第 %d 行：重复数据已跳过", lineNo))
			continue
		}
		seen[dedupeKey] = struct{}{}
		pairs = append(pairs, InventoryPair{Email: email, APIKey: key})
	}
	return pairs, warnings
}

func splitInventoryLine(line string) (email, apiKey string, ok bool) {
	for _, tok := range inventorySplitTokens {
		if strings.Contains(line, tok) {
			parts := strings.SplitN(line, tok, 2)
			if len(parts) != 2 {
				continue
			}
			a, b := strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])
			return classifyEmailKey(a, b)
		}
	}
	if strings.Contains(line, "\t") {
		fields := firstTwoNonEmptyFields(strings.Split(line, "\t"))
		if len(fields) >= 2 {
			return classifyEmailKey(fields[0], fields[1])
		}
	}
	r := csv.NewReader(strings.NewReader(line))
	r.TrimLeadingSpace = true
	rec, err := r.Read()
	if err != nil || len(rec) < 2 {
		return "", "", false
	}
	fields := firstTwoNonEmptyFields(rec)
	if len(fields) < 2 {
		return "", "", false
	}
	return classifyEmailKey(fields[0], fields[1])
}

func classifyEmailKey(a, b string) (email, apiKey string, ok bool) {
	a = strings.TrimSpace(a)
	b = strings.TrimSpace(b)
	aEmail := isLikelyInventoryEmail(a)
	bEmail := isLikelyInventoryEmail(b)
	aAt := strings.Contains(a, "@")
	bAt := strings.Contains(b, "@")
	switch {
	case aEmail && !bEmail:
		return a, b, true
	case bEmail && !aEmail:
		return b, a, true
	case aAt && !bAt:
		return a, b, true
	case bAt && !aAt:
		return b, a, true
	case aAt && bAt:
		return "", "", false
	default:
		return "", "", false
	}
}

func isLikelyInventoryEmail(value string) bool {
	return inventoryEmailRe.MatchString(strings.TrimSpace(value))
}

func firstTwoNonEmptyFields(fields []string) []string {
	out := make([]string, 0, 2)
	for _, field := range fields {
		v := normalizeInventoryHeaderToken(field)
		if v == "" {
			continue
		}
		out = append(out, strings.TrimSpace(strings.Trim(field, `"'`)))
		if len(out) == 2 {
			break
		}
	}
	return out
}

func isInventoryHeaderLine(line string) bool {
	cells := firstTwoNonEmptyFields(strings.Split(line, "\t"))
	if len(cells) < 2 {
		r := csv.NewReader(strings.NewReader(line))
		r.TrimLeadingSpace = true
		if rec, err := r.Read(); err == nil {
			cells = firstTwoNonEmptyFields(rec)
		}
	}
	if len(cells) < 2 {
		return false
	}
	a := normalizeInventoryHeaderToken(cells[0])
	b := normalizeInventoryHeaderToken(cells[1])
	return (containsAnyToken(a, inventoryHeaderEmailTokens) && containsAnyToken(b, inventoryHeaderKeyTokens)) ||
		(containsAnyToken(b, inventoryHeaderEmailTokens) && containsAnyToken(a, inventoryHeaderKeyTokens))
}

func normalizeInventoryHeaderToken(value string) string {
	s := strings.TrimSpace(strings.Trim(value, `"'`))
	replacer := strings.NewReplacer(" ", "", "_", "", "-", "", "\u3000", "", "\t", "")
	return strings.ToLower(replacer.Replace(s))
}

func containsAnyToken(value string, tokens []string) bool {
	for _, token := range tokens {
		if value == normalizeInventoryHeaderToken(token) {
			return true
		}
	}
	return false
}

func (s *Store) GetClaudeShopConfig(ctx context.Context) (*ClaudeShopConfig, error) {
	var c ClaudeShopConfig
	err := s.pool.QueryRow(ctx, `
		SELECT enabled, title, subtitle, description, tutorial_url,
		       retail_price_cents, wholesale_min_qty, wholesale_price_cents,
		       tag_hot, show_tag_wholesale, tag_fan_welfare, max_per_user,
		       wechat_qr_file, alipay_qr_file,
		       static_payment_manual_confirm, static_qr_enabled
		FROM claude_shop_config WHERE id = 1`,
	).Scan(
		&c.Enabled, &c.Title, &c.Subtitle, &c.Description, &c.TutorialURL,
		&c.RetailPriceCents, &c.WholesaleMinQty, &c.WholesalePriceCents,
		&c.TagHot, &c.ShowTagWholesale, &c.TagFanWelfare, &c.MaxPerUser,
		&c.WechatQRFile, &c.AlipayQRFile, &c.StaticPaymentManualConfirm, &c.StaticQREnabled,
	)
	if err != nil {
		return nil, err
	}
	return &c, nil
}

func (s *Store) UpdateClaudeShopConfig(ctx context.Context, c *ClaudeShopConfig) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE claude_shop_config SET
		  enabled = $1, title = $2, subtitle = $3, description = $4, tutorial_url = $5,
		  retail_price_cents = $6, wholesale_min_qty = $7, wholesale_price_cents = $8,
		  tag_hot = $9, show_tag_wholesale = $10, tag_fan_welfare = $11, max_per_user = $12,
		  wechat_qr_file = $13, alipay_qr_file = $14,
		  static_payment_manual_confirm = $15, static_qr_enabled = $16, updated_at = NOW()
		WHERE id = 1`,
		c.Enabled, c.Title, c.Subtitle, c.Description, c.TutorialURL,
		c.RetailPriceCents, c.WholesaleMinQty, c.WholesalePriceCents,
		c.TagHot, c.ShowTagWholesale, c.TagFanWelfare, c.MaxPerUser,
		c.WechatQRFile, c.AlipayQRFile, c.StaticPaymentManualConfirm, c.StaticQREnabled,
	)
	return err
}

func (s *Store) SetClaudeShopQRFile(ctx context.Context, field string, filename string) error {
	if field == "wechat" {
		_, err := s.pool.Exec(ctx, `UPDATE claude_shop_config SET wechat_qr_file = $1, updated_at = NOW() WHERE id = 1`, filename)
		return err
	}
	if field == "alipay" {
		_, err := s.pool.Exec(ctx, `UPDATE claude_shop_config SET alipay_qr_file = $1, updated_at = NOW() WHERE id = 1`, filename)
		return err
	}
	return fmt.Errorf("invalid qr field")
}

func (s *Store) CountClaudeInventoryAvailable(ctx context.Context) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx, `SELECT COUNT(*)::int FROM claude_inventory WHERE status = 'available'`).Scan(&n)
	return n, err
}

// CountClaudeInventoryAvailableFor 返回某订单实际可用库存（混合池方案）。
// productID 非空：同 product_id 专属池 + 通用池（product_id IS NULL）两部分之和（订单可兜底取用）。
// productID 为空：仅通用池，用于无 SKU（单店模式）订单或老订单。
func (s *Store) CountClaudeInventoryAvailableFor(ctx context.Context, productID *uuid.UUID) (int, error) {
	var n int
	if productID == nil {
		err := s.pool.QueryRow(ctx,
			`SELECT COUNT(*)::int FROM claude_inventory WHERE status = 'available' AND product_id IS NULL`,
		).Scan(&n)
		return n, err
	}
	err := s.pool.QueryRow(ctx,
		`SELECT COUNT(*)::int FROM claude_inventory
		 WHERE status = 'available' AND (product_id = $1 OR product_id IS NULL)`,
		*productID,
	).Scan(&n)
	return n, err
}

// GetProductStockMap 返回每 SKU 的可用库存统计（分专属池 / 含通用池兜底）以及通用池本身的数量。
// unassigned 为 product_id IS NULL 的待售数；products 键为 SKU id，值为 dedicated + with_unassigned。
func (s *Store) GetProductStockMap(ctx context.Context) (products map[string]model.ClaudeProductStock, unassigned int, err error) {
	products = map[string]model.ClaudeProductStock{}
	if err = s.pool.QueryRow(ctx,
		`SELECT COUNT(*)::int FROM claude_inventory WHERE status = 'available' AND product_id IS NULL`,
	).Scan(&unassigned); err != nil {
		return nil, 0, err
	}
	rows, err := s.pool.Query(ctx, `
		SELECT product_id, COUNT(*)::int
		FROM claude_inventory
		WHERE status = 'available' AND product_id IS NOT NULL
		GROUP BY product_id`)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	for rows.Next() {
		var pid uuid.UUID
		var n int
		if err = rows.Scan(&pid, &n); err != nil {
			return nil, 0, err
		}
		pidCopy := pid
		products[pid.String()] = model.ClaudeProductStock{
			ProductID:      &pidCopy,
			Dedicated:      n,
			WithUnassigned: n + unassigned,
		}
	}
	return products, unassigned, rows.Err()
}

func (s *Store) GetClaudeInventorySummary(ctx context.Context) (*model.ClaudeInventorySummary, error) {
	var summary model.ClaudeInventorySummary
	err := s.pool.QueryRow(ctx, `
		SELECT
			COUNT(*)::int AS total,
			COUNT(*) FILTER (WHERE status = 'available')::int AS available,
			COUNT(*) FILTER (WHERE status = 'sold')::int AS sold
		FROM claude_inventory`,
	).Scan(&summary.Total, &summary.Available, &summary.Sold)
	if err != nil {
		return nil, err
	}
	return &summary, nil
}

func normalizeClaudeInventoryStatusFilter(status string) (string, bool) {
	switch strings.TrimSpace(strings.ToLower(status)) {
	case "", "all":
		return "", true
	case "available", "sold":
		return strings.TrimSpace(strings.ToLower(status)), true
	default:
		return "", false
	}
}

// ListClaudeInventory 管理端库存列表。
// productFilter: ""=不过滤；"__none__"=仅通用池（product_id IS NULL）；否则按 UUID 过滤。
func (s *Store) ListClaudeInventory(ctx context.Context, statusFilter, batchFilter, productFilter string, page, size int) ([]model.ClaudeInventoryItem, int, error) {
	filter, ok := normalizeClaudeInventoryStatusFilter(statusFilter)
	if !ok {
		return nil, 0, fmt.Errorf("invalid_inventory_status")
	}
	batchFilter = strings.TrimSpace(batchFilter)
	productFilter = strings.TrimSpace(productFilter)
	offset := (page - 1) * size

	conds := []string{"1=1"}
	args := []interface{}{}
	argI := 1
	if filter != "" {
		conds = append(conds, fmt.Sprintf("status = $%d", argI))
		args = append(args, filter)
		argI++
	}
	if batchFilter != "" {
		if batchFilter == "__none__" {
			conds = append(conds, "(batch_label IS NULL OR batch_label = '')")
		} else {
			conds = append(conds, fmt.Sprintf("batch_label = $%d", argI))
			args = append(args, batchFilter)
			argI++
		}
	}
	if productFilter != "" {
		if productFilter == "__none__" {
			conds = append(conds, "product_id IS NULL")
		} else {
			pid, perr := uuid.Parse(productFilter)
			if perr != nil {
				return nil, 0, fmt.Errorf("invalid_product_filter")
			}
			conds = append(conds, fmt.Sprintf("product_id = $%d", argI))
			args = append(args, pid)
			argI++
		}
	}
	whereSQL := strings.Join(conds, " AND ")

	var total int
	err := s.pool.QueryRow(ctx, "SELECT COUNT(*)::int FROM claude_inventory WHERE "+whereSQL, args...).Scan(&total)
	if err != nil {
		return nil, 0, err
	}

	dataArgs := append(append([]interface{}{}, args...), size, offset)
	rows, err := s.pool.Query(ctx, fmt.Sprintf(`
		SELECT id, email, api_key, status, order_id::text, COALESCE(batch_label, ''), product_id, created_at
		FROM claude_inventory
		WHERE %s
		ORDER BY created_at DESC
		LIMIT $%d OFFSET $%d`, whereSQL, argI, argI+1),
		dataArgs...,
	)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var list []model.ClaudeInventoryItem
	for rows.Next() {
		var item model.ClaudeInventoryItem
		var orderIDText *string
		var prodID *uuid.UUID
		if err := rows.Scan(&item.ID, &item.Email, &item.APIKey, &item.Status, &orderIDText, &item.BatchLabel, &prodID, &item.CreatedAt); err != nil {
			return nil, 0, err
		}
		if orderIDText != nil && strings.TrimSpace(*orderIDText) != "" {
			if orderID, err := uuid.Parse(*orderIDText); err == nil {
				item.OrderID = &orderID
			}
		}
		if prodID != nil {
			pidCopy := *prodID
			item.ProductID = &pidCopy
		}
		list = append(list, item)
	}
	return list, total, rows.Err()
}

func (s *Store) DeleteClaudeInventory(ctx context.Context, id uuid.UUID) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM claude_inventory WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}

// ImportClaudeInventory 批量导入卡券。productID 为 nil 表示通用池（任意 SKU 订单都能兜底取用）。
func (s *Store) ImportClaudeInventory(ctx context.Context, pairs []InventoryPair, batchLabel string, productID *uuid.UUID) (int, error) {
	if len(pairs) == 0 {
		return 0, nil
	}
	batchLabel = strings.TrimSpace(batchLabel)
	if len(batchLabel) > 64 {
		return 0, fmt.Errorf("invalid_batch_label")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)
	n := 0
	for _, p := range pairs {
		_, err := tx.Exec(ctx,
			`INSERT INTO claude_inventory (email, api_key, batch_label, product_id) VALUES ($1, $2, $3, $4)`,
			p.Email, p.APIKey, batchLabel, productID,
		)
		if err != nil {
			return n, err
		}
		n++
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}
	return n, nil
}

// ListClaudeInventoryBatchSummaries 非空批次汇总 + 无批次且仍为待售的数量
func (s *Store) ListClaudeInventoryBatchSummaries(ctx context.Context) ([]model.ClaudeInventoryBatchInfo, int, error) {
	var unbatchedAvail int
	if err := s.pool.QueryRow(ctx,
		`SELECT COUNT(*)::int FROM claude_inventory WHERE status = 'available' AND (batch_label IS NULL OR batch_label = '')`,
	).Scan(&unbatchedAvail); err != nil {
		return nil, 0, err
	}
	rows, err := s.pool.Query(ctx, `
		SELECT batch_label,
			COUNT(*)::int AS total,
			COUNT(*) FILTER (WHERE status = 'available')::int AS available
		FROM claude_inventory
		WHERE batch_label IS NOT NULL AND batch_label <> ''
		GROUP BY batch_label
		ORDER BY MAX(created_at) DESC
	`)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var out []model.ClaudeInventoryBatchInfo
	for rows.Next() {
		var b model.ClaudeInventoryBatchInfo
		if err := rows.Scan(&b.Label, &b.Total, &b.Available); err != nil {
			return nil, 0, err
		}
		out = append(out, b)
	}
	return out, unbatchedAvail, rows.Err()
}

// PurgeClaudeInventoryBatchAvailable 删除指定批次下所有「待售」记录（已售出不动）
func (s *Store) PurgeClaudeInventoryBatchAvailable(ctx context.Context, batchKey string) (int64, error) {
	batchKey = strings.TrimSpace(batchKey)
	if batchKey == "" {
		return 0, fmt.Errorf("empty_batch")
	}
	var tag pgconn.CommandTag
	var err error
	if batchKey == "__none__" {
		tag, err = s.pool.Exec(ctx, `DELETE FROM claude_inventory WHERE status = 'available' AND (batch_label IS NULL OR batch_label = '')`)
	} else {
		tag, err = s.pool.Exec(ctx, `DELETE FROM claude_inventory WHERE status = 'available' AND batch_label = $1`, batchKey)
	}
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// PurgeAllClaudeInventoryAvailable 删除全部待售库存
func (s *Store) PurgeAllClaudeInventoryAvailable(ctx context.Context) (int64, error) {
	tag, err := s.pool.Exec(ctx, `DELETE FROM claude_inventory WHERE status = 'available'`)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

func (s *Store) SumFulfilledClaudeQuantityForAccount(ctx context.Context, accountID uuid.UUID) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx,
		`SELECT COALESCE(SUM(quantity), 0)::int FROM claude_orders WHERE account_id = $1 AND status = 'fulfilled'`,
		accountID,
	).Scan(&n)
	return n, err
}

// CreateClaudeOrder 创建待支付订单（不预占库存）。paymentChannel: static | alipay_precreate；存在启用 SKU 时 productID 必填
func (s *Store) CreateClaudeOrder(ctx context.Context, accountID uuid.UUID, quantity int, paymentChannel string, productID *uuid.UUID) (*model.ClaudeOrder, error) {
	cfg, err := s.GetClaudeShopConfig(ctx)
	if err != nil {
		return nil, err
	}
	if !cfg.Enabled {
		return nil, fmt.Errorf("shop_disabled")
	}
	if quantity < 1 || quantity > 999 {
		return nil, fmt.Errorf("invalid_quantity")
	}
	// 混合池的精确预检放在解析完 productID 之后（见后文 CountClaudeInventoryAvailableFor）；
	// 实际扣库存由 FulfillClaudeOrder 的 FOR UPDATE SKIP LOCKED 保证一致性。
	if cfg.MaxPerUser > 0 {
		bought, err := s.SumFulfilledClaudeQuantityForAccount(ctx, accountID)
		if err != nil {
			return nil, err
		}
		if bought+quantity > cfg.MaxPerUser {
			return nil, fmt.Errorf("exceeds_purchase_limit")
		}
	}
	paymentChannel = strings.TrimSpace(strings.ToLower(paymentChannel))
	if paymentChannel == "" {
		paymentChannel = "static"
	}
	if paymentChannel != "static" && paymentChannel != "alipay_precreate" {
		return nil, fmt.Errorf("invalid_payment_channel")
	}
	// 启用静态收款码时：仅「静态码」路径限制同一账号一笔待确认；当面付可并行多笔待支付
	if cfg.StaticQREnabled && paymentChannel == "static" {
		var pendingN int
		if err := s.pool.QueryRow(ctx,
			`SELECT COUNT(*)::int FROM claude_orders WHERE account_id = $1 AND status = 'awaiting_payment' AND payment_channel = 'static'`,
			accountID,
		).Scan(&pendingN); err != nil {
			return nil, err
		}
		if pendingN > 0 {
			return nil, fmt.Errorf("pending_order_exists")
		}
	}

	nProd, err := s.CountEnabledClaudeShopProducts(ctx)
	if err != nil {
		return nil, err
	}
	var titleSnap string
	var prodRef *uuid.UUID
	var unit int
	var wholesale bool
	if nProd > 0 {
		if productID == nil {
			return nil, fmt.Errorf("product_required")
		}
		p, err := s.GetClaudeShopProductByID(ctx, *productID, true)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return nil, fmt.Errorf("invalid_product")
			}
			return nil, err
		}
		pid := p.ID
		prodRef = &pid
		titleSnap = p.Title
		wholesale = quantity >= p.WholesaleMinQty
		unit = p.RetailPriceCents
		if wholesale {
			unit = p.WholesalePriceCents
		}
	} else {
		titleSnap = strings.TrimSpace(cfg.Title)
		if titleSnap == "" {
			titleSnap = "Claude 账号"
		}
		wholesale = quantity >= cfg.WholesaleMinQty
		unit = cfg.RetailPriceCents
		if wholesale {
			unit = cfg.WholesalePriceCents
		}
	}
	// 按选中商品的"专属池+通用池"（或无 SKU 时只看通用池）再精确预检一次
	availFor, err := s.CountClaudeInventoryAvailableFor(ctx, prodRef)
	if err != nil {
		return nil, err
	}
	if availFor < quantity {
		return nil, fmt.Errorf("insufficient_stock")
	}
	total := unit * quantity

	var id uuid.UUID
	err = s.pool.QueryRow(ctx, `
		INSERT INTO claude_orders (account_id, quantity, unit_price_cents, total_cents, is_wholesale, status, payment_channel, product_id, product_title_snapshot)
		VALUES ($1, $2, $3, $4, $5, 'awaiting_payment', $6, $7, $8)
		RETURNING id`,
		accountID, quantity, unit, total, wholesale, paymentChannel, prodRef, titleSnap,
	).Scan(&id)
	if err != nil {
		return nil, err
	}
	return s.getClaudeOrderByID(ctx, id)
}

// GetClaudeOrderByID 管理端按 ID 查询（含已发货的 lines）
func (s *Store) GetClaudeOrderByID(ctx context.Context, id uuid.UUID) (*model.ClaudeOrder, error) {
	return s.getClaudeOrderByID(ctx, id)
}

func (s *Store) getClaudeOrderByID(ctx context.Context, id uuid.UUID) (*model.ClaudeOrder, error) {
	var o model.ClaudeOrder
	var alipayTN sql.NullString
	var prodID *uuid.UUID
	err := s.pool.QueryRow(ctx, `
		SELECT id, account_id, quantity, unit_price_cents, total_cents, is_wholesale, status,
		       payment_channel, alipay_trade_no, product_id, product_title_snapshot, created_at, fulfilled_at
		FROM claude_orders WHERE id = $1`, id,
	).Scan(&o.ID, &o.AccountID, &o.Quantity, &o.UnitPriceCents, &o.TotalCents, &o.IsWholesale, &o.Status,
		&o.PaymentChannel, &alipayTN, &prodID, &o.ProductTitleSnapshot, &o.CreatedAt, &o.FulfilledAt)
	if err != nil {
		return nil, err
	}
	if prodID != nil {
		o.ProductID = prodID
	}
	if alipayTN.Valid && strings.TrimSpace(alipayTN.String) != "" {
		s := alipayTN.String
		o.AlipayTradeNo = &s
	}
	if o.Status == "fulfilled" {
		lines, err := s.listClaudeOrderLines(ctx, id)
		if err != nil {
			return nil, err
		}
		o.Lines = lines
	}
	return &o, nil
}

func (s *Store) listClaudeOrderLines(ctx context.Context, orderID uuid.UUID) ([]model.ClaudeOrderLine, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT line_index, email, api_key FROM claude_order_lines WHERE order_id = $1 ORDER BY line_index ASC`,
		orderID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.ClaudeOrderLine
	for rows.Next() {
		var ln model.ClaudeOrderLine
		if err := rows.Scan(&ln.LineIndex, &ln.Email, &ln.APIKey); err != nil {
			return nil, err
		}
		out = append(out, ln)
	}
	return out, rows.Err()
}

// GetClaudeOrderForAccount 用户查看自己的订单（发货后含 lines）
func (s *Store) GetClaudeOrderForAccount(ctx context.Context, orderID, accountID uuid.UUID) (*model.ClaudeOrder, error) {
	o, err := s.getClaudeOrderByID(ctx, orderID)
	if err != nil {
		return nil, err
	}
	if o.AccountID != accountID {
		return nil, pgx.ErrNoRows
	}
	return o, nil
}

func (s *Store) ListClaudeOrdersForAccount(ctx context.Context, accountID uuid.UUID, page, size int) ([]model.ClaudeOrder, int, error) {
	var total int
	err := s.pool.QueryRow(ctx, `SELECT COUNT(*)::int FROM claude_orders WHERE account_id = $1`, accountID).Scan(&total)
	if err != nil {
		return nil, 0, err
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id, account_id, quantity, unit_price_cents, total_cents, is_wholesale, status,
		       payment_channel, alipay_trade_no, product_id, product_title_snapshot, created_at, fulfilled_at
		FROM claude_orders WHERE account_id = $1
		ORDER BY created_at DESC LIMIT $2 OFFSET $3`,
		accountID, size, (page-1)*size,
	)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var list []model.ClaudeOrder
	for rows.Next() {
		var o model.ClaudeOrder
		var alipayTN sql.NullString
		var prodID *uuid.UUID
		if err := rows.Scan(&o.ID, &o.AccountID, &o.Quantity, &o.UnitPriceCents, &o.TotalCents, &o.IsWholesale, &o.Status,
			&o.PaymentChannel, &alipayTN, &prodID, &o.ProductTitleSnapshot, &o.CreatedAt, &o.FulfilledAt); err != nil {
			return nil, 0, err
		}
		if prodID != nil {
			o.ProductID = prodID
		}
		if alipayTN.Valid && strings.TrimSpace(alipayTN.String) != "" {
			s := alipayTN.String
			o.AlipayTradeNo = &s
		}
		list = append(list, o)
	}
	return list, total, rows.Err()
}

func (s *Store) ListClaudeOrdersAdmin(ctx context.Context, statusFilter string, page, size int) ([]model.ClaudeOrder, int, error) {
	var total int
	var err error
	var rows pgx.Rows
	if strings.TrimSpace(statusFilter) != "" {
		err = s.pool.QueryRow(ctx, `SELECT COUNT(*)::int FROM claude_orders WHERE status = $1`, statusFilter).Scan(&total)
		if err != nil {
			return nil, 0, err
		}
		rows, err = s.pool.Query(ctx, `
			SELECT id, account_id, quantity, unit_price_cents, total_cents, is_wholesale, status,
			       payment_channel, alipay_trade_no, product_id, product_title_snapshot, created_at, fulfilled_at
			FROM claude_orders WHERE status = $1
			ORDER BY created_at ASC LIMIT $2 OFFSET $3`,
			statusFilter, size, (page-1)*size,
		)
	} else {
		err = s.pool.QueryRow(ctx, `SELECT COUNT(*)::int FROM claude_orders`).Scan(&total)
		if err != nil {
			return nil, 0, err
		}
		rows, err = s.pool.Query(ctx, `
			SELECT id, account_id, quantity, unit_price_cents, total_cents, is_wholesale, status,
			       payment_channel, alipay_trade_no, product_id, product_title_snapshot, created_at, fulfilled_at
			FROM claude_orders ORDER BY created_at DESC LIMIT $1 OFFSET $2`,
			size, (page-1)*size,
		)
	}
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var list []model.ClaudeOrder
	for rows.Next() {
		var o model.ClaudeOrder
		var alipayTN sql.NullString
		var prodID *uuid.UUID
		if err := rows.Scan(&o.ID, &o.AccountID, &o.Quantity, &o.UnitPriceCents, &o.TotalCents, &o.IsWholesale, &o.Status,
			&o.PaymentChannel, &alipayTN, &prodID, &o.ProductTitleSnapshot, &o.CreatedAt, &o.FulfilledAt); err != nil {
			return nil, 0, err
		}
		if prodID != nil {
			o.ProductID = prodID
		}
		if alipayTN.Valid && strings.TrimSpace(alipayTN.String) != "" {
			s := alipayTN.String
			o.AlipayTradeNo = &s
		}
		list = append(list, o)
	}
	return list, total, rows.Err()
}

// DeleteClaudeAwaitingPaymentOrder 删除仍处于待支付的订单（如 precreate 失败回滚）
func (s *Store) DeleteClaudeAwaitingPaymentOrder(ctx context.Context, orderID uuid.UUID) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM claude_orders WHERE id = $1 AND status = 'awaiting_payment'`, orderID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}

// SetClaudeOrderAlipayTradeNo 在发货前写入支付宝交易号（幂等：已相同则跳过）
func (s *Store) SetClaudeOrderAlipayTradeNo(ctx context.Context, orderID uuid.UUID, tradeNo string) error {
	tradeNo = strings.TrimSpace(tradeNo)
	if tradeNo == "" {
		return fmt.Errorf("empty trade no")
	}
	ct, err := s.pool.Exec(ctx, `
		UPDATE claude_orders SET alipay_trade_no = $2
		WHERE id = $1 AND status = 'awaiting_payment'
		  AND (alipay_trade_no IS NULL OR alipay_trade_no = '' OR alipay_trade_no = $2)`,
		orderID, tradeNo,
	)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		return fmt.Errorf("order_not_awaiting_or_trade_mismatch")
	}
	return nil
}

// FulfillClaudeOrder 管理员/支付回调确认收款后扣库存并发货（混合池：先订单 SKU 专属池，再通用池兜底）
func (s *Store) FulfillClaudeOrder(ctx context.Context, orderID uuid.UUID) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	var status string
	var qty int
	var prodID *uuid.UUID
	err = tx.QueryRow(ctx,
		`SELECT status, quantity, product_id FROM claude_orders WHERE id = $1 FOR UPDATE`,
		orderID,
	).Scan(&status, &qty, &prodID)
	if err != nil {
		return err
	}
	if status != "awaiting_payment" {
		return fmt.Errorf("order_not_awaiting_payment")
	}

	type pick struct {
		id     uuid.UUID
		email  string
		apiKey string
	}
	// Step 1: 同 SKU 专属池优先（订单无 SKU 时跳过）
	var picks []pick
	if prodID != nil {
		rows, err := tx.Query(ctx, `
			SELECT id, email, api_key FROM claude_inventory
			WHERE status = 'available' AND product_id = $1
			ORDER BY created_at ASC LIMIT $2 FOR UPDATE SKIP LOCKED`,
			*prodID, qty,
		)
		if err != nil {
			return err
		}
		for rows.Next() {
			var p pick
			if err := rows.Scan(&p.id, &p.email, &p.apiKey); err != nil {
				rows.Close()
				return err
			}
			picks = append(picks, p)
		}
		rows.Close()
	}
	// Step 2: 通用池兜底（product_id IS NULL）；订单无 SKU 时全部从此来源
	if len(picks) < qty {
		rest := qty - len(picks)
		rows, err := tx.Query(ctx, `
			SELECT id, email, api_key FROM claude_inventory
			WHERE status = 'available' AND product_id IS NULL
			ORDER BY created_at ASC LIMIT $1 FOR UPDATE SKIP LOCKED`,
			rest,
		)
		if err != nil {
			return err
		}
		for rows.Next() {
			var p pick
			if err := rows.Scan(&p.id, &p.email, &p.apiKey); err != nil {
				rows.Close()
				return err
			}
			picks = append(picks, p)
		}
		rows.Close()
	}
	if len(picks) < qty {
		return fmt.Errorf("insufficient_stock")
	}

	for i, p := range picks {
		if _, err := tx.Exec(ctx, `UPDATE claude_inventory SET status = 'sold', order_id = $1 WHERE id = $2`, orderID, p.id); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO claude_order_lines (order_id, line_index, email, api_key) VALUES ($1, $2, $3, $4)`,
			orderID, i, p.email, p.apiKey,
		); err != nil {
			return err
		}
	}

	if _, err := tx.Exec(ctx, `UPDATE claude_orders SET status = 'fulfilled', fulfilled_at = NOW() WHERE id = $1`, orderID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// CountEnabledClaudeShopProducts 启用的 SKU 数量（>0 时下单须指定 product_id）
func (s *Store) CountEnabledClaudeShopProducts(ctx context.Context) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx, `SELECT COUNT(*)::int FROM claude_shop_products WHERE enabled = TRUE`).Scan(&n)
	return n, err
}

func scanClaudeShopProduct(row interface{ Scan(...interface{}) error }) (*model.ClaudeShopProduct, error) {
	var p model.ClaudeShopProduct
	if err := row.Scan(&p.ID, &p.SortOrder, &p.Enabled, &p.Title, &p.Description, &p.Tag,
		&p.RetailPriceCents, &p.WholesaleMinQty, &p.WholesalePriceCents, &p.CreatedAt, &p.UpdatedAt); err != nil {
		return nil, err
	}
	return &p, nil
}

// GetClaudeShopProductByID 查询 SKU；enabledOnly 为 true 时仅返回启用行
func (s *Store) GetClaudeShopProductByID(ctx context.Context, id uuid.UUID, enabledOnly bool) (*model.ClaudeShopProduct, error) {
	q := `SELECT id, sort_order, enabled, title, description, tag, retail_price_cents, wholesale_min_qty, wholesale_price_cents, created_at, updated_at
		FROM claude_shop_products WHERE id = $1`
	if enabledOnly {
		q += ` AND enabled = TRUE`
	}
	return scanClaudeShopProduct(s.pool.QueryRow(ctx, q, id))
}

// ListClaudeShopProductsPublic 用户侧仅启用 SKU
func (s *Store) ListClaudeShopProductsPublic(ctx context.Context) ([]model.ClaudeShopProduct, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, sort_order, enabled, title, description, tag, retail_price_cents, wholesale_min_qty, wholesale_price_cents, created_at, updated_at
		FROM claude_shop_products WHERE enabled = TRUE
		ORDER BY sort_order ASC, created_at ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.ClaudeShopProduct
	for rows.Next() {
		p, err := scanClaudeShopProduct(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *p)
	}
	return out, rows.Err()
}

// ListClaudeShopProductsAdmin 管理端全部 SKU
func (s *Store) ListClaudeShopProductsAdmin(ctx context.Context) ([]model.ClaudeShopProduct, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, sort_order, enabled, title, description, tag, retail_price_cents, wholesale_min_qty, wholesale_price_cents, created_at, updated_at
		FROM claude_shop_products
		ORDER BY sort_order ASC, created_at ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.ClaudeShopProduct
	for rows.Next() {
		p, err := scanClaudeShopProduct(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *p)
	}
	return out, rows.Err()
}

// InsertClaudeShopProduct 新建 SKU
func (s *Store) InsertClaudeShopProduct(ctx context.Context, p *model.ClaudeShopProduct) error {
	return s.pool.QueryRow(ctx, `
		INSERT INTO claude_shop_products (sort_order, enabled, title, description, tag, retail_price_cents, wholesale_min_qty, wholesale_price_cents)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
		RETURNING id, created_at, updated_at`,
		p.SortOrder, p.Enabled, p.Title, p.Description, p.Tag, p.RetailPriceCents, p.WholesaleMinQty, p.WholesalePriceCents,
	).Scan(&p.ID, &p.CreatedAt, &p.UpdatedAt)
}

// UpdateClaudeShopProduct 更新 SKU（按 id）
func (s *Store) UpdateClaudeShopProduct(ctx context.Context, p *model.ClaudeShopProduct) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE claude_shop_products SET
		  sort_order = $2, enabled = $3, title = $4, description = $5, tag = $6,
		  retail_price_cents = $7, wholesale_min_qty = $8, wholesale_price_cents = $9, updated_at = NOW()
		WHERE id = $1`,
		p.ID, p.SortOrder, p.Enabled, p.Title, p.Description, p.Tag,
		p.RetailPriceCents, p.WholesaleMinQty, p.WholesalePriceCents,
	)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}

// DeleteClaudeShopProduct 删除 SKU（订单 product_id 会置空）
func (s *Store) DeleteClaudeShopProduct(ctx context.Context, id uuid.UUID) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM claude_shop_products WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}
