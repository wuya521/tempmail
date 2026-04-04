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
	CreatedAt            time.Time  `json:"created_at"`
	FulfilledAt          *time.Time `json:"fulfilled_at,omitempty"`
	Lines                []ClaudeOrderLine `json:"lines,omitempty"`
}

// ClaudeShopProduct 店铺 SKU（与全局库存共用池，区别在展示名称与单价）
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
	CreatedAt           time.Time `json:"created_at"`
	UpdatedAt           time.Time `json:"updated_at"`
}

type ClaudeOrderLine struct {
	LineIndex int    `json:"line_index"`
	Email     string `json:"email"`
	APIKey    string `json:"api_key"`
}

type ClaudeInventoryItem struct {
	ID         uuid.UUID  `json:"id"`
	Email      string     `json:"email"`
	APIKey     string     `json:"api_key"`
	Status     string     `json:"status"`
	OrderID    *uuid.UUID `json:"order_id,omitempty"`
	BatchLabel string     `json:"batch_label"`
	CreatedAt  time.Time  `json:"created_at"`
}

// ClaudeInventoryBatchInfo 非空 batch_label 的汇总（用于管理端筛选）
type ClaudeInventoryBatchInfo struct {
	Label     string `json:"label"`
	Total     int    `json:"total"`
	Available int    `json:"available"`
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
