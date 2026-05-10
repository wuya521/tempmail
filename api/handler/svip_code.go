package handler

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"tempmail/middleware"
	"tempmail/store"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
)

type SVIPCodeHandler struct {
	store *store.Store
}

func NewSVIPCodeHandler(s *store.Store) *SVIPCodeHandler {
	return &SVIPCodeHandler{store: s}
}

func svipCodeErrorMessage(err error) (int, string) {
	switch {
	case errors.Is(err, store.ErrSVIPActivationCodeInvalid):
		return http.StatusBadRequest, "激活码不存在或格式不正确"
	case errors.Is(err, store.ErrSVIPActivationCodeDisabled):
		return http.StatusBadRequest, "这个激活码已停用"
	case errors.Is(err, store.ErrSVIPActivationCodeExpired):
		return http.StatusBadRequest, "这个激活码已过期"
	case errors.Is(err, store.ErrSVIPActivationCodeUsedUp):
		return http.StatusBadRequest, "这个激活码已被使用完"
	case errors.Is(err, store.ErrSVIPActivationCodeAlreadyRedeemed):
		return http.StatusBadRequest, "你已经兑换过这个激活码"
	default:
		return http.StatusInternalServerError, err.Error()
	}
}

// POST /api/svip/redeem  body: { "code": "SVIP-XXXX-XXXX-XXXX" }
func (h *SVIPCodeHandler) Redeem(c *gin.Context) {
	acc := middleware.GetAccount(c)
	if acc == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}
	var req struct {
		Code string `json:"code" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请输入激活码"})
		return
	}
	account, code, err := h.store.RedeemSVIPActivationCode(c.Request.Context(), acc.ID, req.Code)
	if err != nil {
		status, msg := svipCodeErrorMessage(err)
		c.JSON(status, gin.H{"error": msg})
		return
	}
	if svipGiftHook != nil {
		svipGiftHook(c, h.store, account)
	}
	c.JSON(http.StatusOK, gin.H{
		"message": "SVIP 激活成功",
		"account": account,
		"code":    code,
	})
}

// GET /api/admin/svip-codes
func (h *SVIPCodeHandler) AdminList(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	size, _ := strconv.Atoi(c.DefaultQuery("size", "30"))
	list, total, err := h.store.ListSVIPActivationCodes(c.Request.Context(), page, size)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"items": list,
		"total": total,
		"page":  page,
		"size":  size,
	})
}

// POST /api/admin/svip-codes/generate
func (h *SVIPCodeHandler) AdminGenerate(c *gin.Context) {
	var req struct {
		Count        int     `json:"count"`
		DurationDays int     `json:"duration_days"`
		MaxUses      int     `json:"max_uses"`
		Note         string  `json:"note"`
		ExpiresAt    *string `json:"expires_at"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if req.Count <= 0 {
		req.Count = 1
	}
	if req.MaxUses <= 0 {
		req.MaxUses = 1
	}
	var expiresAt *time.Time
	if req.ExpiresAt != nil && strings.TrimSpace(*req.ExpiresAt) != "" {
		t, err := time.Parse(time.RFC3339, strings.TrimSpace(*req.ExpiresAt))
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "expires_at 必须是 RFC3339 时间"})
			return
		}
		expiresAt = &t
	}
	items, err := h.store.GenerateSVIPActivationCodes(c.Request.Context(), store.SVIPActivationCodeCreateOptions{
		Count:        req.Count,
		Level:        1,
		DurationDays: req.DurationDays,
		MaxUses:      req.MaxUses,
		Note:         req.Note,
		ExpiresAt:    expiresAt,
	})
	if err != nil {
		switch err.Error() {
		case "invalid_count":
			c.JSON(http.StatusBadRequest, gin.H{"error": "生成数量需在 1-200 之间"})
		case "invalid_duration_days":
			c.JSON(http.StatusBadRequest, gin.H{"error": "有效期天数不能小于 0"})
		case "invalid_max_uses":
			c.JSON(http.StatusBadRequest, gin.H{"error": "每码可用次数需在 1-1000 之间"})
		case "invalid_note":
			c.JSON(http.StatusBadRequest, gin.H{"error": "备注最长 160 字符"})
		default:
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		}
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": items, "total": len(items)})
}

// POST /api/admin/svip-codes/generate-and-list
// 批量生成 SVIP 兑换码并作为库存上架到指定商品
func (h *SVIPCodeHandler) AdminGenerateAndList(c *gin.Context) {
	var req struct {
		Count        int     `json:"count"`
		DurationDays int     `json:"duration_days"`
		MaxUses      int     `json:"max_uses"`
		Note         string  `json:"note"`
		ExpiresAt    *string `json:"expires_at"`
		ProductID    string  `json:"product_id" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if req.Count <= 0 {
		req.Count = 1
	}
	if req.MaxUses <= 0 {
		req.MaxUses = 1
	}

	pid, perr := parseUUID(strings.TrimSpace(req.ProductID))
	if perr != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "product_id 无效"})
		return
	}

	var expiresAt *time.Time
	if req.ExpiresAt != nil && strings.TrimSpace(*req.ExpiresAt) != "" {
		t, err := time.Parse(time.RFC3339, strings.TrimSpace(*req.ExpiresAt))
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "expires_at 必须是 RFC3339 时间"})
			return
		}
		expiresAt = &t
	}

	ctx := c.Request.Context()
	items, err := h.store.GenerateSVIPActivationCodes(ctx, store.SVIPActivationCodeCreateOptions{
		Count:        req.Count,
		Level:        1,
		DurationDays: req.DurationDays,
		MaxUses:      req.MaxUses,
		Note:         req.Note,
		ExpiresAt:    expiresAt,
	})
	if err != nil {
		switch err.Error() {
		case "invalid_count":
			c.JSON(http.StatusBadRequest, gin.H{"error": "生成数量需在 1-200 之间"})
		case "invalid_duration_days":
			c.JSON(http.StatusBadRequest, gin.H{"error": "有效期天数不能小于 0"})
		case "invalid_max_uses":
			c.JSON(http.StatusBadRequest, gin.H{"error": "每码可用次数需在 1-1000 之间"})
		case "invalid_note":
			c.JSON(http.StatusBadRequest, gin.H{"error": "备注最长 160 字符"})
		default:
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		}
		return
	}

	payloads := make([]map[string]interface{}, 0, len(items))
	for _, item := range items {
		payloads = append(payloads, map[string]interface{}{
			"text": "SVIP兑换码: " + item.Code,
		})
	}
	batch := "svip-" + time.Now().Format("0102-150405")
	n, importErr := h.store.ImportClaudeInventoryPayload(ctx, payloads, batch, &pid, "text")
	if importErr != nil {
		c.JSON(http.StatusOK, gin.H{
			"items":        items,
			"total":        len(items),
			"import_error": importErr.Error(),
		})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"items":    items,
		"total":    len(items),
		"imported": n,
		"batch":    batch,
	})
}

// PATCH /api/admin/svip-codes/:id/toggle  body: { "enabled": true }
func (h *SVIPCodeHandler) AdminToggle(c *gin.Context) {
	id, err := parseUUID(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}
	var req struct {
		Enabled bool `json:"enabled"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	code, err := h.store.SetSVIPActivationCodeEnabled(c.Request.Context(), id, req.Enabled)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "ok", "code": code})
}
