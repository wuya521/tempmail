package handler

import (
	"log"
	"net/http"

	"tempmail/store"

	"github.com/gin-gonic/gin"
)

type RegisterHandler struct {
	store *store.Store
}

func NewRegisterHandler(s *store.Store) *RegisterHandler {
	return &RegisterHandler{store: s}
}

// POST /public/register → 公开注册（仅当 registration_open=true 时可用）
func (h *RegisterHandler) Register(c *gin.Context) {
	// 检查注册开关
	regOpen, err := h.store.GetSetting(c.Request.Context(), "registration_open")
	if err != nil || regOpen != "true" {
		c.JSON(http.StatusForbidden, gin.H{"error": "registration is currently closed"})
		return
	}

	var req struct {
		Username string `json:"username" binding:"required,min=2,max=64"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	account, err := h.store.CreateAccount(c.Request.Context(), req.Username)
	if err != nil {
		c.JSON(http.StatusConflict, gin.H{"error": "username already exists"})
		return
	}

	ctx := c.Request.Context()
	resp := gin.H{
		"id":       account.ID,
		"username": account.Username,
		"api_key":  account.APIKey,
		"message":  "registration successful — save your API key, it won't be shown again",
	}

	// 自动创建首个邮箱：顾客用 api_key 登录 Web 即可立刻收验证码，无需先手动建邮箱
	if mb, err := TryCreateWelcomeMailbox(ctx, h.store, account.ID); err != nil {
		log.Printf("[register] welcome mailbox skipped for %s: %v", account.Username, err)
		resp["mailbox"] = nil
		resp["mailbox_note"] = "no welcome mailbox: ensure at least one active domain exists; you can create one via POST /api/mailboxes after login"
	} else {
		resp["mailbox"] = mb
		resp["message"] = "registration successful — save your API key; first mailbox created, use it for verification emails"
	}

	c.JSON(http.StatusCreated, resp)
}
