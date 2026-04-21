package handler

import (
	"errors"
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

type CouponHandler struct {
	store *store.Store
}

func NewCouponHandler(s *store.Store) *CouponHandler {
	return &CouponHandler{store: s}
}

// ==================== 用户端 ====================

// GET /api/coupons/mine?status=available|used|expired|all
func (h *CouponHandler) MyList(c *gin.Context) {
	acc := middleware.GetAccount(c)
	status := strings.TrimSpace(c.DefaultQuery("status", "all"))
	list, err := h.store.ListMyCoupons(c.Request.Context(), acc.ID, status)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"data":   list,
		"status": status,
	})
}

// POST /api/coupons/redeem  body: { "code": "XXX" }
func (h *CouponHandler) Redeem(c *gin.Context) {
	acc := middleware.GetAccount(c)
	var req struct {
		Code string `json:"code" binding:"required,min=1,max=64"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	uc, err := h.store.RedeemCoupon(c.Request.Context(), acc.ID, strings.TrimSpace(req.Code), acc.IsSVIP())
	if err != nil {
		switch {
		case errors.Is(err, store.ErrCouponNotFound):
			c.JSON(http.StatusNotFound, gin.H{"error": "优惠码不存在或已失效"})
		case errors.Is(err, store.ErrCouponExpired):
			c.JSON(http.StatusGone, gin.H{"error": "优惠码已过期"})
		case errors.Is(err, store.ErrCouponQuotaExhausted):
			c.JSON(http.StatusConflict, gin.H{"error": "优惠码已被领完"})
		case errors.Is(err, store.ErrCouponPerUserExceeded):
			c.JSON(http.StatusConflict, gin.H{"error": "您已达到该优惠码的领取次数上限"})
		case errors.Is(err, store.ErrCouponSVIPOnly):
			c.JSON(http.StatusForbidden, gin.H{"error": "此优惠码仅限 SVIP 会员领取"})
		case errors.Is(err, store.ErrCouponOwnedAvailable):
			c.JSON(http.StatusConflict, gin.H{"error": "您已持有一张未使用的同款优惠券，请先使用后再领取"})
		default:
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		}
		return
	}
	log.Printf("[coupon] user %s redeemed code=%s uc=%s", acc.Username, req.Code, uc.ID)
	c.JSON(http.StatusOK, gin.H{
		"message": "领取成功！",
		"user_coupon": uc,
	})
}

// POST /api/coupons/quote  body: { "user_coupon_id": "...", "original_cents": 19900 }
//
//	返回折扣预览，供下单页实时显示优惠金额
func (h *CouponHandler) Quote(c *gin.Context) {
	acc := middleware.GetAccount(c)
	var req struct {
		UserCouponID  string `json:"user_coupon_id" binding:"required"`
		OriginalCents int    `json:"original_cents" binding:"required,min=0"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	ucid, err := parseUUID(req.UserCouponID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "user_coupon_id 无效"})
		return
	}
	quote, err := h.store.QuoteDiscount(c.Request.Context(), acc.ID, ucid, req.OriginalCents)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, quote)
}

// ==================== 管理员 ====================

// GET /api/admin/coupons?status=all|enabled|disabled|expired&q=xxx&page=1&size=20
func (h *CouponHandler) AdminList(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	size, _ := strconv.Atoi(c.DefaultQuery("size", "20"))
	if page < 1 {
		page = 1
	}
	if size < 1 || size > 100 {
		size = 20
	}
	filter := store.CouponListFilter{
		Search: c.Query("q"),
		Status: c.Query("status"),
	}
	list, total, err := h.store.ListCoupons(c.Request.Context(), page, size, filter)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"data":  list,
		"total": total,
		"page":  page,
		"size":  size,
	})
}

// adminCouponReq 创建/更新公共请求体（更新时省略 id，走 URL 参数）
type adminCouponReq struct {
	Code             *string `json:"code"` // 空字符串或 null = 无公开码
	Name             string  `json:"name" binding:"required,min=1,max=160"`
	Description      string  `json:"description"`
	DiscountType     string  `json:"discount_type" binding:"required,oneof=percentage fixed"`
	DiscountValue    int     `json:"discount_value" binding:"required,min=1"`
	MinOrderCents    int     `json:"min_order_cents"`
	MaxDiscountCents int     `json:"max_discount_cents"`
	TotalQuota       int     `json:"total_quota"`
	PerUserLimit     int     `json:"per_user_limit"`
	StartsAt         *string `json:"starts_at"`  // RFC3339
	ExpiresAt        *string `json:"expires_at"` // RFC3339
	SVIPOnly         bool    `json:"svip_only"`
	NewUserGift      bool    `json:"new_user_gift"`
	SVIPGift         bool    `json:"svip_gift"`
	Enabled          *bool   `json:"enabled"`
}

func parseRFC3339Ptr(s *string) (*time.Time, error) {
	if s == nil {
		return nil, nil
	}
	v := strings.TrimSpace(*s)
	if v == "" {
		return nil, nil
	}
	t, err := time.Parse(time.RFC3339, v)
	if err != nil {
		return nil, err
	}
	return &t, nil
}

func (req *adminCouponReq) toModel(existing *model.Coupon) (*model.Coupon, error) {
	m := &model.Coupon{
		Name:             strings.TrimSpace(req.Name),
		Description:      req.Description,
		DiscountType:     req.DiscountType,
		DiscountValue:    req.DiscountValue,
		MinOrderCents:    req.MinOrderCents,
		MaxDiscountCents: req.MaxDiscountCents,
		TotalQuota:       req.TotalQuota,
		PerUserLimit:     req.PerUserLimit,
		SVIPOnly:         req.SVIPOnly,
		NewUserGift:      req.NewUserGift,
		SVIPGift:         req.SVIPGift,
		Enabled:          true,
	}
	if req.PerUserLimit <= 0 {
		m.PerUserLimit = 1
	}
	if req.Enabled != nil {
		m.Enabled = *req.Enabled
	}
	if req.Code != nil {
		code := strings.TrimSpace(*req.Code)
		if code != "" {
			m.Code = &code
		}
	}
	sa, err := parseRFC3339Ptr(req.StartsAt)
	if err != nil {
		return nil, errors.New("starts_at 必须为 RFC3339 格式")
	}
	ea, err := parseRFC3339Ptr(req.ExpiresAt)
	if err != nil {
		return nil, errors.New("expires_at 必须为 RFC3339 格式")
	}
	m.StartsAt = sa
	m.ExpiresAt = ea

	if existing != nil {
		m.ID = existing.ID
		m.UsedCount = existing.UsedCount
		m.CreatedAt = existing.CreatedAt
	}
	return m, nil
}

// POST /api/admin/coupons
func (h *CouponHandler) AdminCreate(c *gin.Context) {
	var req adminCouponReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	m, err := req.toModel(nil)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	out, err := h.store.CreateCoupon(c.Request.Context(), m)
	if err != nil {
		if strings.Contains(err.Error(), "duplicate key") {
			c.JSON(http.StatusConflict, gin.H{"error": "领取码重复"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, gin.H{"coupon": out})
}

// PUT /api/admin/coupons/:id
func (h *CouponHandler) AdminUpdate(c *gin.Context) {
	id, err := parseUUID(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}
	cur, err := h.store.GetCouponByID(c.Request.Context(), id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "优惠券不存在"})
		return
	}
	var req adminCouponReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	m, err := req.toModel(cur)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	out, err := h.store.UpdateCoupon(c.Request.Context(), m)
	if err != nil {
		if strings.Contains(err.Error(), "duplicate key") {
			c.JSON(http.StatusConflict, gin.H{"error": "领取码重复"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"coupon": out})
}

// PATCH /api/admin/coupons/:id/toggle  body: { "enabled": true|false }
func (h *CouponHandler) AdminToggle(c *gin.Context) {
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
	if err := h.store.SetCouponEnabled(c.Request.Context(), id, req.Enabled); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "ok"})
}

// DELETE /api/admin/coupons/:id
func (h *CouponHandler) AdminDelete(c *gin.Context) {
	id, err := parseUUID(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}
	if err := h.store.DeleteCoupon(c.Request.Context(), id); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "ok"})
}

// POST /api/admin/coupons/:id/grant  body: { "account_ids": ["uuid", ...] }
//
//	定向派发：允许一次指定多个账户
func (h *CouponHandler) AdminGrant(c *gin.Context) {
	id, err := parseUUID(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}
	var req struct {
		AccountIDs []string `json:"account_ids" binding:"required,min=1"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	ctx := c.Request.Context()
	type perResult struct {
		AccountID string `json:"account_id"`
		OK        bool   `json:"ok"`
		Error     string `json:"error,omitempty"`
	}
	results := make([]perResult, 0, len(req.AccountIDs))
	ok := 0
	for _, s := range req.AccountIDs {
		s = strings.TrimSpace(s)
		aid, perr := uuid.Parse(s)
		if perr != nil {
			results = append(results, perResult{AccountID: s, OK: false, Error: "invalid uuid"})
			continue
		}
		_, gerr := h.store.GrantCouponDirect(ctx, aid, id)
		if gerr != nil {
			results = append(results, perResult{AccountID: s, OK: false, Error: gerr.Error()})
			continue
		}
		results = append(results, perResult{AccountID: s, OK: true})
		ok++
	}
	c.JSON(http.StatusOK, gin.H{
		"granted": ok,
		"total":   len(req.AccountIDs),
		"results": results,
	})
}

// GrantSVIPGiftsHook 供 handler/account.go 在授权 SVIP 后调用
func GrantSVIPGiftsHook(ctx *gin.Context, s *store.Store, account *model.Account) {
	if account == nil || account.SVIPLevel <= 0 {
		return
	}
	n, err := s.GrantAutoGifts(ctx.Request.Context(), account.ID, "svip")
	if err != nil {
		log.Printf("[coupon] svip_gift grant error: %v", err)
		return
	}
	if n > 0 {
		log.Printf("[coupon] svip_gift auto-granted %d coupons to %s", n, account.Username)
	}
}

// GrantNewUserGifts 注册成功后调用（由 register 模块注入）
func GrantNewUserGifts(ctx *gin.Context, s *store.Store, accountID uuid.UUID) {
	n, err := s.GrantAutoGifts(ctx.Request.Context(), accountID, "new_user")
	if err != nil {
		log.Printf("[coupon] new_user gift error: %v", err)
		return
	}
	if n > 0 {
		log.Printf("[coupon] new_user gift auto-granted %d coupons to %s", n, accountID)
	}
}
