package handler

import (
	"log"
	"net/http"
	"strconv"

	"tempmail/middleware"
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
func (h *AccountHandler) Create(c *gin.Context) {
	var req struct {
		Username string `json:"username" binding:"required,min=2,max=64"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	account, err := h.store.CreateAccount(c.Request.Context(), req.Username)
	if err != nil {
		c.JSON(http.StatusConflict, gin.H{"error": "username already exists or db error: " + err.Error()})
		return
	}

	ctx := c.Request.Context()
	out := gin.H{
		"id":       account.ID,
		"username": account.Username,
		"api_key":  account.APIKey,
	}
	if mb, err := TryCreateWelcomeMailbox(ctx, h.store, account.ID); err != nil {
		log.Printf("[admin create account] welcome mailbox skipped for %s: %v", account.Username, err)
		out["mailbox"] = nil
		out["mailbox_note"] = "no welcome mailbox: ensure at least one active domain exists"
	} else {
		out["mailbox"] = mb
	}

	c.JSON(http.StatusCreated, out)
}

// GET /api/admin/accounts - 列出所有账号（管理员）
func (h *AccountHandler) List(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	size, _ := strconv.Atoi(c.DefaultQuery("size", "20"))
	if page < 1 { page = 1 }
	if size < 1 || size > 100 { size = 20 }

	accounts, total, err := h.store.ListAccounts(c.Request.Context(), page, size)
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

// GET /api/me - 查看当前账号信息
func (h *AccountHandler) Me(c *gin.Context) {
	account := middleware.GetAccount(c)
	c.JSON(http.StatusOK, gin.H{
		"id":         account.ID,
		"username":   account.Username,
		"is_admin":   account.IsAdmin,
		"created_at": account.CreatedAt,
	})
}
