package handler

import (
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"tempmail/middleware"
	"tempmail/model"
	"tempmail/store"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type AccountHandler struct {
	store *store.Store
}

func NewAccountHandler(s *store.Store) *AccountHandler {
	return &AccountHandler{store: s}
}

// POST /api/admin/accounts - 创建账号（管理员）
// 可选 mailbox_domain：指定已激活域名时，自动创建随机本地部分的邮箱（全局唯一冲突则换随机串重试）。
// 不传 mailbox_domain 则仅创建账号，不创建邮箱。
func (h *AccountHandler) Create(c *gin.Context) {
	var req struct {
		Username      string `json:"username" binding:"required,min=2,max=64"`
		MailboxDomain string `json:"mailbox_domain"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	ctx := c.Request.Context()
	var dom *model.Domain
	if d := strings.TrimSpace(strings.ToLower(req.MailboxDomain)); d != "" {
		found, err := h.store.GetDomainByName(ctx, d)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "mailbox_domain: domain not found or not active: " + d})
			return
		}
		dom = found
	}

	account, err := h.store.CreateAccount(ctx, req.Username)
	if err != nil {
		c.JSON(http.StatusConflict, gin.H{"error": "username already exists or db error: " + err.Error()})
		return
	}

	// v10：管理员创建的账户也按"新用户"处理，发放 new_user_gift 券
	GrantNewUserGifts(c, h.store, account.ID)

	out := gin.H{
		"id":       account.ID,
		"username": account.Username,
		"api_key":  account.APIKey,
	}
	if dom != nil {
		if mb, err := TryCreateMailboxForDomain(ctx, h.store, account.ID, dom); err != nil {
			log.Printf("[admin create account] mailbox under %s failed for %s: %v", dom.Domain, account.Username, err)
			out["mailbox"] = nil
			out["mailbox_error"] = err.Error()
		} else {
			out["mailbox"] = mb
		}
	}

	c.JSON(http.StatusCreated, out)
}

// GET /api/admin/accounts - 列出所有账号（管理员）
// v10：新增 status 查询参数（all/active/banned/svip）配合前端筛选胶囊
func (h *AccountHandler) List(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	size, _ := strconv.Atoi(c.DefaultQuery("size", "10"))
	if page < 1 {
		page = 1
	}
	if size < 1 || size > 100 {
		size = 10
	}

	filter := store.AccountListFilter{
		Status: c.Query("status"),
		Search: c.Query("q"),
	}

	accounts, total, err := h.store.ListAccounts(c.Request.Context(), page, size, filter)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"data":   accounts,
		"total":  total,
		"page":   page,
		"size":   size,
		"status": filter.Status,
	})
}

// DELETE /api/admin/accounts/:id - 删除账号（管理员）
func (h *AccountHandler) Delete(c *gin.Context) {
	id, err := parseUUID(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid account id"})
		return
	}

	if err := h.store.DeleteAccount(c.Request.Context(), id); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "account deleted"})
}

// PATCH /api/admin/accounts/:id  body: { "is_active": true|false } 封禁=停用登录
func (h *AccountHandler) Patch(c *gin.Context) {
	id, err := parseUUID(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid account id"})
		return
	}
	var req struct {
		IsActive *bool `json:"is_active" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	self := middleware.GetAccount(c)
	if id == self.ID && !*req.IsActive {
		c.JSON(http.StatusForbidden, gin.H{"error": "不能封禁当前登录账号"})
		return
	}
	adminTarget, err := h.store.GetAccountByID(c.Request.Context(), id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "账户不存在"})
		return
	}
	if adminTarget.IsAdmin && !*req.IsActive {
		c.JSON(http.StatusForbidden, gin.H{"error": "不能封禁管理员账号"})
		return
	}
	_ = self
	if err := h.store.SetAccountActive(c.Request.Context(), id, *req.IsActive); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "ok"})
}

// GET /api/me - 查看当前账号信息
func (h *AccountHandler) Me(c *gin.Context) {
	account := middleware.GetAccount(c)
	out := gin.H{
		"id":                       account.ID,
		"username":                 account.Username,
		"is_admin":                 account.IsAdmin,
		"created_at":               account.CreatedAt,
		"last_seen_at":             account.LastSeenAt,
		"svip_level":               account.SVIPLevel,
		"svip_expires_at":          account.SVIPExpiresAt,
		"mailbox_quota":            account.MailboxQuota,
		"mailbox_ttl_minutes":      account.MailboxTTLMinutes,
		"exclusive_fan_level":      account.ExclusiveFanLevel,
		"exclusive_fan_claimed_at": account.ExclusiveFanClaimedAt,
	}
	c.JSON(http.StatusOK, out)
}

// POST /api/me/rotate-key - 当前用户自助重置 API Key。
// 成功后旧 key 立即失效，新 key 仅在此响应中一次性返回，前端需保存并替换 localStorage。
func (h *AccountHandler) MyRotateKey(c *gin.Context) {
	account := middleware.GetAccount(c)
	oldPrefix := apiKeyPrefix(account.APIKey)
	newKey, err := h.store.RotateAPIKey(c.Request.Context(), account.ID)
	if err != nil {
		log.Printf("[rotate-key] self uid=%s username=%s failed: %v", account.ID, account.Username, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	log.Printf("[rotate-key] self uid=%s username=%s old_prefix=%s new_prefix=%s",
		account.ID, account.Username, oldPrefix, apiKeyPrefix(newKey))
	c.JSON(http.StatusOK, gin.H{
		"api_key": newKey,
		"message": "API Key 已轮换，旧 Key 立即失效；请妥善保存新 Key，它不会再次展示。",
	})
}

// POST /api/admin/accounts/:id/rotate-key - 管理员为指定账号重置 API Key。
// 新 key 仅在响应里一次性返回，管理员需尽快通知用户；禁止对自己使用（请用 /api/me/rotate-key）。
func (h *AccountHandler) AdminRotateKey(c *gin.Context) {
	id, err := parseUUID(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid account id"})
		return
	}
	self := middleware.GetAccount(c)
	if id == self.ID {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请使用 /api/me/rotate-key 重置自己的 Key"})
		return
	}
	target, err := h.store.GetAccountByID(c.Request.Context(), id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "账户不存在"})
		return
	}
	oldPrefix := apiKeyPrefix(target.APIKey)
	newKey, err := h.store.RotateAPIKey(c.Request.Context(), id)
	if err != nil {
		log.Printf("[rotate-key] admin by=%s target=%s failed: %v", self.Username, target.Username, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	log.Printf("[rotate-key] admin by=%s target=%s old_prefix=%s new_prefix=%s",
		self.Username, target.Username, oldPrefix, apiKeyPrefix(newKey))
	c.JSON(http.StatusOK, gin.H{
		"id":       target.ID,
		"username": target.Username,
		"api_key":  newKey,
		"message":  "新 Key 仅在此展示一次，请立即告知用户并提醒其妥善保存。",
	})
}

// apiKeyPrefix 日志脱敏：仅保留前 10 位（tm_ + 7 字符 hex），足以审计、无法还原。
func apiKeyPrefix(k string) string {
	if len(k) <= 10 {
		return k
	}
	return k[:10] + "…"
}

// ==================== v10：SVIP & 配额 ====================

// svipGiftHook SVIP 授权成功后的异步副作用（如发放专属券）。由 main.go 注入，避免 handler 直依赖 coupon 包。
var svipGiftHook func(ctx *gin.Context, s *store.Store, account *model.Account)

// SetSVIPGiftHook 注册授权 SVIP 成功后的回调
func SetSVIPGiftHook(hook func(ctx *gin.Context, s *store.Store, account *model.Account)) {
	svipGiftHook = hook
}

// POST /api/admin/accounts/:id/svip  body: { "level": 1, "expires_at": "2026-12-31T23:59:59Z"|null, "duration_days": 30 }
// 规则：
//   - level > 0 即授权 SVIP（目前仅支持 1）；level <= 0 撤销
//   - expires_at 优先于 duration_days；二者都未传 = 永久
//   - 返回最新账户快照
func (h *AccountHandler) GrantSVIP(c *gin.Context) {
	id, err := parseUUID(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid account id"})
		return
	}
	var req struct {
		Level        int     `json:"level"`
		ExpiresAt    *string `json:"expires_at"`
		DurationDays *int    `json:"duration_days"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	ctx := c.Request.Context()
	if req.Level <= 0 {
		if err := h.store.RevokeSVIP(ctx, id); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		acc, _ := h.store.GetAccountByID(ctx, id)
		c.JSON(http.StatusOK, gin.H{"message": "SVIP 已撤销", "account": acc})
		return
	}

	var expiresAt *time.Time
	if req.ExpiresAt != nil && strings.TrimSpace(*req.ExpiresAt) != "" {
		t, parseErr := time.Parse(time.RFC3339, *req.ExpiresAt)
		if parseErr != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "expires_at 必须为 RFC3339 格式"})
			return
		}
		expiresAt = &t
	} else if req.DurationDays != nil && *req.DurationDays > 0 {
		t := time.Now().Add(time.Duration(*req.DurationDays) * 24 * time.Hour)
		expiresAt = &t
	}

	acc, err := h.store.GrantSVIP(ctx, id, req.Level, expiresAt)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	if svipGiftHook != nil {
		svipGiftHook(c, h.store, acc)
	}

	msg := "SVIP 已授权"
	if expiresAt != nil {
		msg += "，到期 " + expiresAt.Format("2006-01-02")
	} else {
		msg += "（永久）"
	}
	c.JSON(http.StatusOK, gin.H{"message": msg, "account": acc})
}

// POST /api/admin/accounts/:id/quota
// body: {
//   "mailbox_quota": -1|0|N,                  // 必填。-1=无限，0=默认，正数=专属上限
//   "mailbox_ttl_minutes": null|0|N,           // 必填。null=默认（跟随全局），0=永久，正数=分钟
//   "use_default_ttl": true|false              // 可选。true 时强制 ttl=NULL（与 mailbox_ttl_minutes=null 同义）
// }
func (h *AccountHandler) SetQuota(c *gin.Context) {
	id, err := parseUUID(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid account id"})
		return
	}
	var req struct {
		MailboxQuota      *int  `json:"mailbox_quota"`
		MailboxTTLMinutes *int  `json:"mailbox_ttl_minutes"`
		UseDefaultTTL     *bool `json:"use_default_ttl"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	ctx := c.Request.Context()
	if req.MailboxQuota != nil {
		if err := h.store.SetMailboxQuota(ctx, id, *req.MailboxQuota); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
	}
	// TTL 更新策略：
	//   - use_default_ttl == true  → ttl = NULL（跟随全局）
	//   - req 中明确传入 mailbox_ttl_minutes → 使用该值（0=永久）
	//   - 都没传 → 不动（跳过 UPDATE）
	shouldSetTTL := false
	var ttlPtr *int
	if req.UseDefaultTTL != nil && *req.UseDefaultTTL {
		shouldSetTTL = true
		ttlPtr = nil
	} else if req.MailboxTTLMinutes != nil {
		shouldSetTTL = true
		ttlPtr = req.MailboxTTLMinutes
	}
	if shouldSetTTL {
		if err := h.store.SetMailboxTTLMinutes(ctx, id, ttlPtr); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
	}
	acc, _ := h.store.GetAccountByID(ctx, id)
	c.JSON(http.StatusOK, gin.H{"message": "配额已更新", "account": acc})
}

// 保留 UUID 引用避免 unused import 报错（SetQuota/GrantSVIP 用的是 parseUUID 间接返回）
var _ = uuid.UUID{}
