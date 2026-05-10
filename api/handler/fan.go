package handler

import (
	"errors"
	"net/http"

	"tempmail/middleware"
	"tempmail/store"

	"github.com/gin-gonic/gin"
)

type FanHandler struct {
	store *store.Store
}

func NewFanHandler(s *store.Store) *FanHandler {
	return &FanHandler{store: s}
}

func fanDiscountFold(discountBps int) float64 {
	return float64(discountBps) / 1000.0
}

// GET /api/fan/status
// 返回当前用户"专属老粉"认证领取状态与门槛。
func (h *FanHandler) Status(c *gin.Context) {
	acc := middleware.GetAccount(c)
	ctx := c.Request.Context()
	cfg := h.store.GetExclusiveFanConfig(ctx)
	doneOrders, err := h.store.CountFulfilledClaudeOrdersForAccount(ctx, acc.ID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	claimed := acc.IsExclusiveFan()
	c.JSON(http.StatusOK, gin.H{
		"enabled":                   cfg.Enabled,
		"min_orders":                cfg.MinOrders,
		"fulfilled_orders":          doneOrders,
		"eligible":                  cfg.Enabled && doneOrders >= cfg.MinOrders,
		"claimed":                   claimed,
		"exclusive_fan_level":       acc.ExclusiveFanLevel,
		"exclusive_fan_claimed_at":  acc.ExclusiveFanClaimedAt,
		"exclusive_fan_discount_bps": cfg.DiscountBps,
		"exclusive_fan_discount_fold": fanDiscountFold(cfg.DiscountBps),
	})
}

// POST /api/fan/claim
// 用户在满足购买次数后自行领取"专属老粉"认证，并自动领取 fan_gift 优惠券。
func (h *FanHandler) Claim(c *gin.Context) {
	acc := middleware.GetAccount(c)
	ctx := c.Request.Context()
	cfg := h.store.GetExclusiveFanConfig(ctx)
	if !cfg.Enabled {
		c.JSON(http.StatusForbidden, gin.H{"error": "专属老粉认证暂未开放领取"})
		return
	}

	updated, newlyClaimed, doneOrders, err := h.store.ClaimExclusiveFan(ctx, acc.ID, cfg.MinOrders)
	if err != nil {
		if errors.Is(err, store.ErrExclusiveFanNotEligible) {
			c.JSON(http.StatusConflict, gin.H{
				"error":            "还差一点点就能领取专属老粉认证",
				"min_orders":       cfg.MinOrders,
				"fulfilled_orders": doneOrders,
			})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	gifted := 0
	if newlyClaimed {
		if n, gerr := h.store.GrantAutoGifts(ctx, updated.ID, "fan"); gerr == nil {
			gifted = n
		}
	}

	msg := "已经是专属老粉"
	if newlyClaimed {
		msg = "专属老粉认证领取成功"
	}
	c.JSON(http.StatusOK, gin.H{
		"message":                    msg,
		"account":                    updated,
		"newly_claimed":              newlyClaimed,
		"gifted_coupons":             gifted,
		"min_orders":                 cfg.MinOrders,
		"fulfilled_orders":           doneOrders,
		"exclusive_fan_discount_bps":  cfg.DiscountBps,
		"exclusive_fan_discount_fold": fanDiscountFold(cfg.DiscountBps),
	})
}

// POST /api/admin/accounts/:id/fan  body: { "level": 1 }  level<=0 撤销
func (h *FanHandler) AdminGrant(c *gin.Context) {
	id, err := parseUUID(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid account id"})
		return
	}
	var req struct {
		Level int `json:"level"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	ctx := c.Request.Context()

	if req.Level <= 0 {
		if err := h.store.AdminRevokeExclusiveFan(ctx, id); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		acc, _ := h.store.GetAccountByID(ctx, id)
		c.JSON(http.StatusOK, gin.H{"message": "老粉认证已撤销", "account": acc})
		return
	}

	acc, newlyGranted, err := h.store.AdminGrantExclusiveFan(ctx, id, req.Level)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	gifted := 0
	if newlyGranted {
		if n, gerr := h.store.GrantAutoGifts(ctx, acc.ID, "fan"); gerr == nil {
			gifted = n
		}
	}

	msg := "已经是老粉"
	if newlyGranted {
		msg = "老粉认证已赠送"
	}
	c.JSON(http.StatusOK, gin.H{
		"message":        msg,
		"account":        acc,
		"newly_granted":  newlyGranted,
		"gifted_coupons": gifted,
	})
}
