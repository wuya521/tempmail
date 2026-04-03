package handler

import (
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
// 仅创建平台账号与 api_key，不自动创建邮箱；用户登录后可自行 POST /api/mailboxes 或由管理员代建。
func (h *RegisterHandler) Register(c *gin.Context) {
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

	c.JSON(http.StatusCreated, gin.H{
		"id":       account.ID,
		"username": account.Username,
		"api_key":  account.APIKey,
		"message":  "registration successful — save your API key, it won't be shown again",
	})
}
