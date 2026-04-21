package model

import (
	"time"

	"github.com/google/uuid"
)

// ==================== 数据模型 ====================

type Account struct {
	ID         uuid.UUID  `json:"id"`
	Username   string     `json:"username"`
	APIKey     string     `json:"api_key"`
	IsAdmin    bool       `json:"is_admin"`
	IsActive   bool       `json:"is_active"`
	CreatedAt  time.Time  `json:"created_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
	LastSeenAt *time.Time `json:"last_seen_at,omitempty"`
	// v10：SVIP + 配额
	SVIPLevel         int        `json:"svip_level"`
	SVIPExpiresAt     *time.Time `json:"svip_expires_at,omitempty"`
	MailboxQuota      int        `json:"mailbox_quota"`              // 0=默认，-1=无限，正数=专属
	MailboxTTLMinutes *int       `json:"mailbox_ttl_minutes,omitempty"` // NULL=默认
}

// IsSVIP 判断当前是否处于有效 SVIP 期
func (a *Account) IsSVIP() bool {
	if a == nil || a.SVIPLevel <= 0 {
		return false
	}
	if a.SVIPExpiresAt != nil && a.SVIPExpiresAt.Before(time.Now()) {
		return false
	}
	return true
}

// ClaudeOrder 自助售号订单
type ClaudeOrder struct {
	ID                   uuid.UUID  `json:"id"`
	AccountID            uuid.UUID  `json:"account_id"`
	Quantity             int        `json:"quantity"`
	UnitPriceCents       int        `json:"unit_price_cents"`
	TotalCents           int        `json:"total_cents"`
	IsWholesale          bool       `json:"is_wholesale"`
	Status               string     `json:"status"`
	PaymentChannel       string     `json:"payment_channel"`
	AlipayTradeNo        *string    `json:"alipay_trade_no,omitempty"`
	ProductID            *uuid.UUID `json:"product_id,omitempty"`
	ProductTitleSnapshot string     `json:"product_title_snapshot,omitempty"`
	// v10：优惠 + SVIP 快照
	OriginalTotalCents int        `json:"original_total_cents"`
	DiscountCents      int        `json:"discount_cents"`
	CouponID           *uuid.UUID `json:"coupon_id,omitempty"`
	CouponCodeSnapshot string     `json:"coupon_code_snapshot,omitempty"`
	SVIPSnapshot       int        `json:"svip_snapshot"`
	CreatedAt          time.Time  `json:"created_at"`
	FulfilledAt        *time.Time `json:"fulfilled_at,omitempty"`
	Lines              []ClaudeOrderLine `json:"lines,omitempty"`
}

// ClaudeShopProduct 店铺 SKU。
// 自 migrate_v9 起，每个 SKU 可绑定独立卡券池（claude_inventory.product_id）；
// 订单取货时先用本 SKU 专属池，不足再从通用池（product_id IS NULL）兜底。
// 自 migrate_v10 起，每个 SKU 可设置发货模式（delivery_type）与 SVIP 专享价（svip_price_cents）。
type ClaudeShopProduct struct {
	ID                  uuid.UUID `json:"id"`
	SortOrder           int       `json:"sort_order"`
	Enabled             bool      `json:"enabled"`
	Title               string    `json:"title"`
	Description         string    `json:"description"`
	Tag                 string    `json:"tag"`
	RetailPriceCents    int       `json:"retail_price_cents"`
	WholesaleMinQty     int       `json:"wholesale_min_qty"`
	WholesalePriceCents int       `json:"wholesale_price_cents"`
	// v10：发货模式 + SVIP 专享价
	DeliveryType     string          `json:"delivery_type"`             // card_key | text | custom_kv
	DeliverySchema   DeliverySchema  `json:"delivery_schema"`           // custom_kv 时字段定义
	SVIPPriceCents   *int            `json:"svip_price_cents,omitempty"` // NULL=不设
	CreatedAt        time.Time       `json:"created_at"`
	UpdatedAt        time.Time       `json:"updated_at"`
}

// DeliverySchema custom_kv 模式的字段定义
type DeliverySchema struct {
	Fields []DeliveryField `json:"fields,omitempty"`
}

type DeliveryField struct {
	Key       string `json:"key"`       // 程序用，如 "url"
	Label     string `json:"label"`     // 展示用，如 "网盘链接"
	Hint      string `json:"hint,omitempty"`
	Multiline bool   `json:"multiline,omitempty"` // true 时前端用 textarea
}

type ClaudeOrderLine struct {
	LineIndex    int                    `json:"line_index"`
	Email        string                 `json:"email"`
	APIKey       string                 `json:"api_key"`
	DeliveryType string                 `json:"delivery_type,omitempty"`
	Payload      map[string]interface{} `json:"payload,omitempty"`
}

type ClaudeInventoryItem struct {
	ID           uuid.UUID              `json:"id"`
	Email        string                 `json:"email"`
	APIKey       string                 `json:"api_key"`
	Status       string                 `json:"status"`
	OrderID      *uuid.UUID             `json:"order_id,omitempty"`
	BatchLabel   string                 `json:"batch_label"`
	ProductID    *uuid.UUID             `json:"product_id,omitempty"` // nil = 通用池
	Payload      map[string]interface{} `json:"payload,omitempty"`
	CreatedAt    time.Time              `json:"created_at"`
}

// ClaudeInventoryBatchInfo 非空 batch_label 的汇总（用于管理端筛选）
type ClaudeInventoryBatchInfo struct {
	Label     string `json:"label"`
	Total     int    `json:"total"`
	Available int    `json:"available"`
}

// ClaudeProductStock 某 SKU 当前可售 = 专属池 + 通用池兜底
type ClaudeProductStock struct {
	ProductID       *uuid.UUID `json:"product_id,omitempty"` // nil = 通用池自身
	Dedicated       int        `json:"dedicated"`            // 本 SKU 专属池可用
	WithUnassigned  int        `json:"with_unassigned"`      // 包含通用池兜底后的可用数
}

type ClaudeInventorySummary struct {
	Total     int `json:"total"`
	Available int `json:"available"`
	Sold      int `json:"sold"`
}

type Domain struct {
	ID           int        `json:"id"`
	Domain       string     `json:"domain"`
	IsActive     bool       `json:"is_active"`
	Status       string     `json:"status"` // active | pending | disabled
	CreatedAt    time.Time  `json:"created_at"`
	MxCheckedAt  *time.Time `json:"mx_checked_at,omitempty"`
}

type Stats struct {
	TotalMailboxes  int `json:"total_mailboxes"`
	ActiveMailboxes int `json:"active_mailboxes"`
	TotalEmails     int `json:"total_emails"`
	ActiveDomains   int `json:"active_domains"`
	PendingDomains  int `json:"pending_domains"`
	TotalAccounts   int `json:"total_accounts"`
}

type Mailbox struct {
	ID          uuid.UUID  `json:"id"`
	AccountID   uuid.UUID  `json:"account_id"`
	Address     string     `json:"address"`
	DomainID    int        `json:"domain_id"`
	FullAddress string     `json:"full_address"`
	CreatedAt   time.Time  `json:"created_at"`
	ExpiresAt   *time.Time `json:"expires_at,omitempty"` // nil = 永不过期
}

type Email struct {
	ID         uuid.UUID `json:"id"`
	MailboxID  uuid.UUID `json:"mailbox_id"`
	Sender     string    `json:"sender"`
	Subject    string    `json:"subject"`
	BodyText   string    `json:"body_text"`
	BodyHTML   string    `json:"body_html"`
	RawMessage string    `json:"raw_message,omitempty"`
	SizeBytes  int       `json:"size_bytes"`
	ReceivedAt time.Time `json:"received_at"`
}

// ==================== 请求/响应 ====================

type CreateAccountReq struct {
	Username string `json:"username" binding:"required,min=2,max=64"`
}

type CreateAccountResp struct {
	ID       uuid.UUID `json:"id"`
	Username string    `json:"username"`
	APIKey   string    `json:"api_key"`
}

type AddDomainReq struct {
	Domain string `json:"domain" binding:"required,fqdn"`
}

type DNSInstruction struct {
	Type     string `json:"type"`
	Host     string `json:"host"`
	Value    string `json:"value"`
	Priority int    `json:"priority,omitempty"`
}

type AddDomainResp struct {
	Domain       Domain           `json:"domain"`
	DNSRecords   []DNSInstruction `json:"dns_records"`
	Instructions string           `json:"instructions"`
}

type CreateMailboxReq struct {
	Address string `json:"address,omitempty"` // 可选，为空则随机生成
}

type CreateMailboxResp struct {
	Mailbox Mailbox `json:"mailbox"`
}

type ListResp[T any] struct {
	Data  []T `json:"data"`
	Total int `json:"total"`
	Page  int `json:"page"`
	Size  int `json:"size"`
}

type EmailSummary struct {
	ID         uuid.UUID `json:"id"`
	Sender     string    `json:"sender"`
	Subject    string    `json:"subject"`
	SizeBytes  int       `json:"size_bytes"`
	ReceivedAt time.Time `json:"received_at"`
}

// ==================== v10：优惠券 ====================

// Coupon 优惠券定义
type Coupon struct {
	ID                uuid.UUID  `json:"id"`
	Code              *string    `json:"code,omitempty"`      // 公开领取码，NULL=仅定向派发
	Name              string     `json:"name"`
	Description       string     `json:"description"`
	DiscountType      string     `json:"discount_type"`       // percentage | fixed
	DiscountValue     int        `json:"discount_value"`      // percentage: 0-100；fixed: 分
	MinOrderCents     int        `json:"min_order_cents"`
	MaxDiscountCents  int        `json:"max_discount_cents"`  // 0=无上限
	TotalQuota        int        `json:"total_quota"`         // 0=无上限
	UsedCount         int        `json:"used_count"`
	PerUserLimit      int        `json:"per_user_limit"`
	StartsAt          *time.Time `json:"starts_at,omitempty"`
	ExpiresAt         *time.Time `json:"expires_at,omitempty"`
	SVIPOnly          bool       `json:"svip_only"`
	NewUserGift       bool       `json:"new_user_gift"`
	SVIPGift          bool       `json:"svip_gift"`
	Enabled           bool       `json:"enabled"`
	CreatedAt         time.Time  `json:"created_at"`
	UpdatedAt         time.Time  `json:"updated_at"`
}

// UserCoupon 用户已领取的券实例
type UserCoupon struct {
	ID          uuid.UUID  `json:"id"`
	AccountID   uuid.UUID  `json:"account_id"`
	CouponID    uuid.UUID  `json:"coupon_id"`
	Status      string     `json:"status"` // available | used | expired | revoked
	OrderID     *uuid.UUID `json:"order_id,omitempty"`
	AcquiredAt  time.Time  `json:"acquired_at"`
	UsedAt      *time.Time `json:"used_at,omitempty"`
	// 冗余快照字段（即使模板被删仍可展示）
	SnapshotName             string     `json:"snapshot_name"`
	SnapshotDiscountType     string     `json:"snapshot_discount_type"`
	SnapshotDiscountValue    int        `json:"snapshot_discount_value"`
	SnapshotMinOrderCents    int        `json:"snapshot_min_order_cents"`
	SnapshotMaxDiscountCents int        `json:"snapshot_max_discount_cents"`
	SnapshotExpiresAt        *time.Time `json:"snapshot_expires_at,omitempty"`
}

// DiscountQuote 折扣计算结果
type DiscountQuote struct {
	OriginalCents int    `json:"original_cents"`
	DiscountCents int    `json:"discount_cents"`
	PayableCents  int    `json:"payable_cents"`
	CouponName    string `json:"coupon_name,omitempty"`
	CouponCode    string `json:"coupon_code,omitempty"`
}
