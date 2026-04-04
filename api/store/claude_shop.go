package store

import (
	"context"
	"encoding/csv"
	"fmt"
	"regexp"
	"strings"

	"tempmail/model"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
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
		       wechat_qr_file, alipay_qr_file
		FROM claude_shop_config WHERE id = 1`,
	).Scan(
		&c.Enabled, &c.Title, &c.Subtitle, &c.Description, &c.TutorialURL,
		&c.RetailPriceCents, &c.WholesaleMinQty, &c.WholesalePriceCents,
		&c.TagHot, &c.ShowTagWholesale, &c.TagFanWelfare, &c.MaxPerUser,
		&c.WechatQRFile, &c.AlipayQRFile,
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
		  wechat_qr_file = $13, alipay_qr_file = $14, updated_at = NOW()
		WHERE id = 1`,
		c.Enabled, c.Title, c.Subtitle, c.Description, c.TutorialURL,
		c.RetailPriceCents, c.WholesaleMinQty, c.WholesalePriceCents,
		c.TagHot, c.ShowTagWholesale, c.TagFanWelfare, c.MaxPerUser,
		c.WechatQRFile, c.AlipayQRFile,
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

func (s *Store) ListClaudeInventory(ctx context.Context, statusFilter string, page, size int) ([]model.ClaudeInventoryItem, int, error) {
	filter, ok := normalizeClaudeInventoryStatusFilter(statusFilter)
	if !ok {
		return nil, 0, fmt.Errorf("invalid_inventory_status")
	}

	var total int
	var rows pgx.Rows
	var err error
	offset := (page - 1) * size
	if filter == "" {
		err = s.pool.QueryRow(ctx, `SELECT COUNT(*)::int FROM claude_inventory`).Scan(&total)
		if err != nil {
			return nil, 0, err
		}
		rows, err = s.pool.Query(ctx, `
			SELECT id, email, api_key, status, order_id::text, created_at
			FROM claude_inventory
			ORDER BY created_at DESC
			LIMIT $1 OFFSET $2`,
			size, offset,
		)
	} else {
		err = s.pool.QueryRow(ctx, `SELECT COUNT(*)::int FROM claude_inventory WHERE status = $1`, filter).Scan(&total)
		if err != nil {
			return nil, 0, err
		}
		rows, err = s.pool.Query(ctx, `
			SELECT id, email, api_key, status, order_id::text, created_at
			FROM claude_inventory
			WHERE status = $1
			ORDER BY created_at DESC
			LIMIT $2 OFFSET $3`,
			filter, size, offset,
		)
	}
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var list []model.ClaudeInventoryItem
	for rows.Next() {
		var item model.ClaudeInventoryItem
		var orderIDText *string
		if err := rows.Scan(&item.ID, &item.Email, &item.APIKey, &item.Status, &orderIDText, &item.CreatedAt); err != nil {
			return nil, 0, err
		}
		if orderIDText != nil && strings.TrimSpace(*orderIDText) != "" {
			if orderID, err := uuid.Parse(*orderIDText); err == nil {
				item.OrderID = &orderID
			}
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

func (s *Store) ImportClaudeInventory(ctx context.Context, pairs []InventoryPair) (int, error) {
	if len(pairs) == 0 {
		return 0, nil
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)
	n := 0
	for _, p := range pairs {
		_, err := tx.Exec(ctx, `INSERT INTO claude_inventory (email, api_key) VALUES ($1, $2)`, p.Email, p.APIKey)
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

func (s *Store) SumFulfilledClaudeQuantityForAccount(ctx context.Context, accountID uuid.UUID) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx,
		`SELECT COALESCE(SUM(quantity), 0)::int FROM claude_orders WHERE account_id = $1 AND status = 'fulfilled'`,
		accountID,
	).Scan(&n)
	return n, err
}

// CreateClaudeOrder 创建待支付订单（不预占库存）
func (s *Store) CreateClaudeOrder(ctx context.Context, accountID uuid.UUID, quantity int) (*model.ClaudeOrder, error) {
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
	avail, err := s.CountClaudeInventoryAvailable(ctx)
	if err != nil {
		return nil, err
	}
	if avail < quantity {
		return nil, fmt.Errorf("insufficient_stock")
	}
	if cfg.MaxPerUser > 0 {
		bought, err := s.SumFulfilledClaudeQuantityForAccount(ctx, accountID)
		if err != nil {
			return nil, err
		}
		if bought+quantity > cfg.MaxPerUser {
			return nil, fmt.Errorf("exceeds_purchase_limit")
		}
	}
	var pendingN int
	if err := s.pool.QueryRow(ctx,
		`SELECT COUNT(*)::int FROM claude_orders WHERE account_id = $1 AND status = 'awaiting_payment'`,
		accountID,
	).Scan(&pendingN); err != nil {
		return nil, err
	}
	if pendingN > 0 {
		return nil, fmt.Errorf("pending_order_exists")
	}
	wholesale := quantity >= cfg.WholesaleMinQty
	unit := cfg.RetailPriceCents
	if wholesale {
		unit = cfg.WholesalePriceCents
	}
	total := unit * quantity

	var id uuid.UUID
	err = s.pool.QueryRow(ctx, `
		INSERT INTO claude_orders (account_id, quantity, unit_price_cents, total_cents, is_wholesale, status)
		VALUES ($1, $2, $3, $4, $5, 'awaiting_payment')
		RETURNING id`,
		accountID, quantity, unit, total, wholesale,
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
	err := s.pool.QueryRow(ctx, `
		SELECT id, account_id, quantity, unit_price_cents, total_cents, is_wholesale, status, created_at, fulfilled_at
		FROM claude_orders WHERE id = $1`, id,
	).Scan(&o.ID, &o.AccountID, &o.Quantity, &o.UnitPriceCents, &o.TotalCents, &o.IsWholesale, &o.Status, &o.CreatedAt, &o.FulfilledAt)
	if err != nil {
		return nil, err
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
		SELECT id, account_id, quantity, unit_price_cents, total_cents, is_wholesale, status, created_at, fulfilled_at
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
		if err := rows.Scan(&o.ID, &o.AccountID, &o.Quantity, &o.UnitPriceCents, &o.TotalCents, &o.IsWholesale, &o.Status, &o.CreatedAt, &o.FulfilledAt); err != nil {
			return nil, 0, err
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
			SELECT id, account_id, quantity, unit_price_cents, total_cents, is_wholesale, status, created_at, fulfilled_at
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
			SELECT id, account_id, quantity, unit_price_cents, total_cents, is_wholesale, status, created_at, fulfilled_at
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
		if err := rows.Scan(&o.ID, &o.AccountID, &o.Quantity, &o.UnitPriceCents, &o.TotalCents, &o.IsWholesale, &o.Status, &o.CreatedAt, &o.FulfilledAt); err != nil {
			return nil, 0, err
		}
		list = append(list, o)
	}
	return list, total, rows.Err()
}

// FulfillClaudeOrder 管理员确认收款后扣库存并发货
func (s *Store) FulfillClaudeOrder(ctx context.Context, orderID uuid.UUID) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	var status string
	var qty int
	err = tx.QueryRow(ctx, `SELECT status, quantity FROM claude_orders WHERE id = $1 FOR UPDATE`, orderID).Scan(&status, &qty)
	if err != nil {
		return err
	}
	if status != "awaiting_payment" {
		return fmt.Errorf("order_not_awaiting_payment")
	}

	rows, err := tx.Query(ctx, `
		SELECT id, email, api_key FROM claude_inventory
		WHERE status = 'available' ORDER BY created_at ASC LIMIT $1 FOR UPDATE SKIP LOCKED`,
		qty,
	)
	if err != nil {
		return err
	}
	type pick struct {
		id     uuid.UUID
		email  string
		apiKey string
	}
	var picks []pick
	for rows.Next() {
		var p pick
		if err := rows.Scan(&p.id, &p.email, &p.apiKey); err != nil {
			rows.Close()
			return err
		}
		picks = append(picks, p)
	}
	rows.Close()
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
