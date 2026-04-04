package handler

import (
	"log"
	"net/http"
	"strconv"
	"strings"

	"tempmail/middleware"
	"tempmail/model"
	"tempmail/store"

	"github.com/gin-gonic/gin"
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
func (h *AccountHandler) List(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	size, _ := strconv.Atoi(c.DefaultQuery("size", "10"))
	if page < 1 {
		page = 1
	}
	if size < 1 || size > 100 {
		size = 10
	}
	q := c.Query("q")

	accounts, total, err := h.store.ListAccounts(c.Request.Context(), page, size, q)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"data":  accounts,
		"total": total,
		"page":  page,
		"size":  size,
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
		"id":           account.ID,
		"username":     account.Username,
		"is_admin":     account.IsAdmin,
		"created_at":   account.CreatedAt,
		"last_seen_at": account.LastSeenAt,
	}
	c.JSON(http.StatusOK, out)
}
