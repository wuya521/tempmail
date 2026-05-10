package handler

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	_ "time/tzdata" // Docker/Alpine 等环境加载时区表，供 Asia/Shanghai 默认批次

	"tempmail/alipay"
	"tempmail/middleware"
	"tempmail/model"
	"tempmail/store"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type ClaudeShopHandler struct {
	store     *store.Store
	assetDir  string
	publicURL string // 如 /public/shop-assets
	alipay    *alipay.Client
	notifyURL string // 与配置一致，用于 precreate
	appID     string // 校验异步通知 app_id
}

func NewClaudeShopHandler(s *store.Store, assetDir string, ap *alipay.Client, notifyURL, appID string) *ClaudeShopHandler {
	return &ClaudeShopHandler{
		store:     s,
		assetDir:  assetDir,
		publicURL: "/public/shop-assets",
		alipay:    ap,
		notifyURL: strings.TrimSpace(notifyURL),
		appID:     strings.TrimSpace(appID),
	}
}

func centsToYuan(c int) float64 {
	return float64(c) / 100
}

func shopProductToResponse(p *model.ClaudeShopProduct) gin.H {
	out := gin.H{
		"id":                    p.ID.String(),
		"sort_order":            p.SortOrder,
		"enabled":               p.Enabled,
		"title":                 p.Title,
		"description":           p.Description,
		"tag":                   p.Tag,
		"retail_price_yuan":     centsToYuan(p.RetailPriceCents),
		"wholesale_price_yuan":  centsToYuan(p.WholesalePriceCents),
		"wholesale_min_qty":     p.WholesaleMinQty,
		"retail_price_cents":    p.RetailPriceCents,
		"wholesale_price_cents": p.WholesalePriceCents,
		"delivery_type":         p.DeliveryType,
		"delivery_schema":       p.DeliverySchema,
		"fixed_content":         p.FixedContent,
		"has_fixed_content":     p.FixedContent != "",
		"created_at":            p.CreatedAt,
		"updated_at":            p.UpdatedAt,
	}
	if p.SVIPPriceCents != nil {
		out["svip_price_cents"] = *p.SVIPPriceCents
		out["svip_price_yuan"] = centsToYuan(*p.SVIPPriceCents)
	}
	return out
}

// GET /public/claude-shop
func (h *ClaudeShopHandler) PublicSummary(c *gin.Context) {
	cfg, err := h.store.GetClaudeShopConfig(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"enabled": false, "error": "shop not initialized"})
		return
	}
	ctx := c.Request.Context()
	stock, _ := h.store.CountClaudeInventoryAvailable(ctx)
	wechatU, alipayU := "", ""
	if cfg.StaticQREnabled {
		wechatU = h.qrURL(cfg.WechatQRFile)
		alipayU = h.qrURL(cfg.AlipayQRFile)
	}
	plist, _ := h.store.ListClaudeShopProductsPublic(ctx)
	if len(plist) == 0 {
		if n, err := h.store.CountClaudeInventoryAvailableFor(ctx, nil); err == nil {
			stock = n
		}
	}
	stockMap, unassigned, _ := h.store.GetProductStockMap(ctx)
	fanCfg := h.store.GetExclusiveFanConfig(ctx)
	products := make([]gin.H, 0, len(plist))
	for _, p := range plist {
		ps := stockMap[p.ID.String()]
		available := ps.WithUnassigned
		if available == 0 && ps.Dedicated > 0 {
			available = ps.Dedicated
		}
		hasFixed := p.FixedContent != ""
		if hasFixed {
			available = 999
		}
		row := gin.H{
			"id":                    p.ID.String(),
			"title":                 p.Title,
			"description":           p.Description,
			"tag":                   p.Tag,
			"retail_price_yuan":     centsToYuan(p.RetailPriceCents),
			"wholesale_min_qty":     p.WholesaleMinQty,
			"wholesale_price_yuan":  centsToYuan(p.WholesalePriceCents),
			"retail_price_cents":    p.RetailPriceCents,
			"wholesale_price_cents": p.WholesalePriceCents,
			"delivery_type":         p.DeliveryType,
			"stock_dedicated":       ps.Dedicated,
			"stock_available":       available,
			"has_fixed_content":     hasFixed,
		}
		if p.SVIPPriceCents != nil {
			row["svip_price_cents"] = *p.SVIPPriceCents
			row["svip_price_yuan"] = centsToYuan(*p.SVIPPriceCents)
		}
		products = append(products, row)
	}
	out := gin.H{
		"enabled":                       cfg.Enabled,
		"title":                         cfg.Title,
		"subtitle":                      cfg.Subtitle,
		"description":                   cfg.Description,
		"tutorial_url":                  cfg.TutorialURL,
		"retail_price_yuan":             centsToYuan(cfg.RetailPriceCents),
		"wholesale_min_qty":           cfg.WholesaleMinQty,
		"wholesale_price_yuan":        centsToYuan(cfg.WholesalePriceCents),
		"tag_hot":                       cfg.TagHot,
		"show_tag_wholesale":            cfg.ShowTagWholesale,
		"tag_fan_welfare":               cfg.TagFanWelfare,
		"max_per_user":                  cfg.MaxPerUser,
		"stock_available":               stock,
		"stock_unassigned":              unassigned,
		"wechat_qr_url":                 wechatU,
		"alipay_qr_url":                 alipayU,
		"retail_price_cents":            cfg.RetailPriceCents,
		"wholesale_price_cents":         cfg.WholesalePriceCents,
		"alipay_precreate_available":    h.alipay != nil && h.notifyURL != "" && h.appID != "",
		"static_payment_manual_confirm": cfg.StaticPaymentManualConfirm,
		"static_qr_enabled":             cfg.StaticQREnabled,
		"products":                      products,
		"product_pick_required":         len(products) > 0,
		"exclusive_fan_enabled":         fanCfg.Enabled,
		"exclusive_fan_min_orders":      fanCfg.MinOrders,
		"exclusive_fan_discount_bps":    fanCfg.DiscountBps,
		"exclusive_fan_discount_fold":   fanDiscountFold(fanCfg.DiscountBps),
	}
	c.JSON(http.StatusOK, out)
}

func (h *ClaudeShopHandler) qrURL(filename string) string {
	if strings.TrimSpace(filename) == "" {
		return ""
	}
	return h.publicURL + "/" + filename
}

// ServeShopAsset GET /public/shop-assets/:filename
func (h *ClaudeShopHandler) ServeShopAsset(c *gin.Context) {
	name := filepath.Base(c.Param("filename"))
	if name == "." || name == "/" || name == "" {
		c.Status(http.StatusNotFound)
		return
	}
	cfg, err := h.store.GetClaudeShopConfig(c.Request.Context())
	if err != nil {
		c.Status(http.StatusNotFound)
		return
	}
	if !cfg.StaticQREnabled {
		c.Status(http.StatusNotFound)
		return
	}
	if name != cfg.WechatQRFile && name != cfg.AlipayQRFile {
		c.Status(http.StatusNotFound)
		return
	}
	c.File(filepath.Join(h.assetDir, name))
}

// POST /public/alipay/notify 支付宝异步通知（无鉴权，验签）
func (h *ClaudeShopHandler) AlipayNotify(c *gin.Context) {
	if h.alipay == nil || h.alipay.PublicKey == nil {
		c.String(http.StatusOK, "failure")
		return
	}
	if err := c.Request.ParseForm(); err != nil {
		c.String(http.StatusOK, "failure")
		return
	}
	params := make(map[string]string, len(c.Request.PostForm))
	for k, v := range c.Request.PostForm {
		if len(v) > 0 {
			params[k] = v[0]
		}
	}
	if err := h.alipay.VerifyNotify(params); err != nil {
		c.String(http.StatusOK, "failure")
		return
	}
	if params["app_id"] != h.appID {
		c.String(http.StatusOK, "failure")
		return
	}
	switch params["trade_status"] {
	case "TRADE_SUCCESS", "TRADE_FINISHED":
	default:
		c.String(http.StatusOK, "success")
		return
	}
	oid := strings.TrimSpace(params["out_trade_no"])
	id, err := uuid.Parse(oid)
	if err != nil {
		c.String(http.StatusOK, "failure")
		return
	}
	ctx := c.Request.Context()
	o, err := h.store.GetClaudeOrderByID(ctx, id)
	if err != nil {
		c.String(http.StatusOK, "failure")
		return
	}
	if o.Status == "fulfilled" {
		c.String(http.StatusOK, "success")
		return
	}
	if o.PaymentChannel != "alipay_precreate" {
		c.String(http.StatusOK, "failure")
		return
	}
	amtStr := strings.TrimSpace(params["total_amount"])
	if amtStr == "" {
		amtStr = strings.TrimSpace(params["buyer_pay_amount"])
	}
	var yuan float64
	nScan, errScan := fmt.Sscanf(amtStr, "%f", &yuan)
	if nScan != 1 || errScan != nil {
		c.String(http.StatusOK, "failure")
		return
	}
	paidCents := int(yuan*100 + 0.5)
	if paidCents != o.TotalCents {
		c.String(http.StatusOK, "failure")
		return
	}
	tradeNo := strings.TrimSpace(params["trade_no"])
	if tradeNo == "" {
		c.String(http.StatusOK, "failure")
		return
	}
	if err := h.store.SetClaudeOrderAlipayTradeNo(ctx, id, tradeNo); err != nil {
		o2, e2 := h.store.GetClaudeOrderByID(ctx, id)
		if e2 == nil && o2 != nil && o2.Status == "fulfilled" {
			c.String(http.StatusOK, "success")
			return
		}
		c.String(http.StatusOK, "failure")
		return
	}
	if err := h.store.FulfillClaudeOrder(ctx, id); err != nil {
		c.String(http.StatusOK, "failure")
		return
	}
	c.String(http.StatusOK, "success")
}

func truncateSubject(s string, max int) string {
	r := []rune(strings.TrimSpace(s))
	if len(r) <= max {
		return string(r)
	}
	return string(r[:max])
}

// GET /api/shop/orders 我的订单
func (h *ClaudeShopHandler) ListMyOrders(c *gin.Context) {
	acc := middleware.GetAccount(c)
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	size, _ := strconv.Atoi(c.DefaultQuery("size", "20"))
	if page < 1 {
		page = 1
	}
	if size < 1 || size > 50 {
		size = 20
	}
	list, total, err := h.store.ListClaudeOrdersForAccount(c.Request.Context(), acc.ID, page, size)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": list, "total": total, "page": page, "size": size})
}

// GET /api/shop/orders/:id
func (h *ClaudeShopHandler) GetMyOrder(c *gin.Context) {
	acc := middleware.GetAccount(c)
	id, err := parseUUID(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid order id"})
		return
	}
	o, err := h.store.GetClaudeOrderForAccount(c.Request.Context(), id, acc.ID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			c.JSON(http.StatusNotFound, gin.H{"error": "order not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	cfg, _ := h.store.GetClaudeShopConfig(c.Request.Context())
	pay := gin.H{"tutorial_url": cfg.TutorialURL}
	if cfg.StaticQREnabled {
		pay["wechat_qr_url"] = h.qrURL(cfg.WechatQRFile)
		pay["alipay_qr_url"] = h.qrURL(cfg.AlipayQRFile)
	} else {
		pay["wechat_qr_url"] = ""
		pay["alipay_qr_url"] = ""
	}
	if o.Status == "awaiting_payment" && o.PaymentChannel == "alipay_precreate" {
		pay["hint"] = "本单为支付宝当面付：若二维码已过期，请稍后在订单页刷新或联系管理员。"
	}
	c.JSON(http.StatusOK, gin.H{"order": o, "payment": pay})
}

// POST /api/shop/orders  body: { "quantity": n, "payment_method": "static" | "alipay", "product_id": "uuid?", "user_coupon_id": "uuid?" }
// v10：新增 user_coupon_id 让用户应用优惠券；SVIP 价由服务端自动判断
func (h *ClaudeShopHandler) CreateOrder(c *gin.Context) {
	acc := middleware.GetAccount(c)
	var req struct {
		Quantity      int     `json:"quantity" binding:"required,min=1,max=999"`
		PaymentMethod string  `json:"payment_method"` // 默认 static；alipay 表示当面付 precreate
		ProductID     *string `json:"product_id"`
		UserCouponID  *string `json:"user_coupon_id"` // v10：用户已领取的优惠券 id
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	pm := strings.ToLower(strings.TrimSpace(req.PaymentMethod))
	if pm == "" {
		pm = "static"
	}
	var payCh string
	switch pm {
	case "static":
		payCh = "static"
	case "alipay":
		payCh = "alipay_precreate"
	default:
		c.JSON(http.StatusBadRequest, gin.H{"error": "payment_method 仅支持 static 或 alipay"})
		return
	}
	if payCh == "alipay_precreate" && (h.alipay == nil || h.notifyURL == "") {
		c.JSON(http.StatusBadRequest, gin.H{"error": "支付宝当面付未配置（ALIPAY_* 环境变量）"})
		return
	}

	ctx := c.Request.Context()
	cfg0, err := h.store.GetClaudeShopConfig(ctx)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if payCh == "static" && !cfg0.StaticQREnabled {
		c.JSON(http.StatusBadRequest, gin.H{"error": "静态收款码已关闭，请使用支付宝当面付"})
		return
	}

	var prodPtr *uuid.UUID
	if req.ProductID != nil && strings.TrimSpace(*req.ProductID) != "" {
		pid, perr := parseUUID(strings.TrimSpace(*req.ProductID))
		if perr != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "product_id 无效"})
			return
		}
		prodPtr = &pid
	}

	var userCouponPtr *uuid.UUID
	if req.UserCouponID != nil && strings.TrimSpace(*req.UserCouponID) != "" {
		ucid, perr := parseUUID(strings.TrimSpace(*req.UserCouponID))
		if perr != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "user_coupon_id 无效"})
			return
		}
		userCouponPtr = &ucid
	}

	fanCfg := h.store.GetExclusiveFanConfig(ctx)
	o, err := h.store.CreateClaudeOrder(ctx, acc.ID, req.Quantity, payCh, prodPtr, store.CreateClaudeOrderOptions{
		SVIPActive:              acc.IsSVIP(),
		ExclusiveFanActive:      acc.IsExclusiveFan() && fanCfg.Enabled,
		ExclusiveFanDiscountBps: fanCfg.DiscountBps,
		UserCouponID:            userCouponPtr,
	})
	if err != nil {
		switch {
		case err.Error() == "shop_disabled":
			c.JSON(http.StatusForbidden, gin.H{"error": "自助购号已关闭"})
		case err.Error() == "insufficient_stock":
			c.JSON(http.StatusBadRequest, gin.H{"error": "库存不足"})
		case err.Error() == "invalid_quantity":
			c.JSON(http.StatusBadRequest, gin.H{"error": "数量无效"})
		case err.Error() == "invalid_payment_channel":
			c.JSON(http.StatusBadRequest, gin.H{"error": "无效的支付渠道"})
		case err.Error() == "exceeds_purchase_limit":
			c.JSON(http.StatusBadRequest, gin.H{"error": "超过每用户限购数量"})
		case err.Error() == "pending_order_exists":
			c.JSON(http.StatusConflict, gin.H{"error": "您已有一笔待管理员确认的静态收款订单，请先处理后再使用静态码下单；或使用支付宝当面付继续购买。"})
		case err.Error() == "product_required":
			c.JSON(http.StatusBadRequest, gin.H{"error": "请先选择商品"})
		case err.Error() == "invalid_product":
			c.JSON(http.StatusBadRequest, gin.H{"error": "商品不存在或已下架"})
		case err.Error() == "invalid_user_coupon":
			c.JSON(http.StatusBadRequest, gin.H{"error": "优惠券不存在或不属于当前账户"})
		case err.Error() == "coupon_not_available":
			c.JSON(http.StatusBadRequest, gin.H{"error": "该优惠券已被使用或已过期"})
		case err.Error() == "coupon_expired":
			c.JSON(http.StatusBadRequest, gin.H{"error": "该优惠券已过期"})
		case err.Error() == "coupon_min_order_not_met":
			c.JSON(http.StatusBadRequest, gin.H{"error": "订单金额未达到该优惠券的使用门槛"})
		case strings.HasPrefix(err.Error(), "coupon_lock_failed"):
			c.JSON(http.StatusConflict, gin.H{"error": "优惠券锁定失败，请刷新页面后重试"})
		default:
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		}
		return
	}

	cfg, _ := h.store.GetClaudeShopConfig(ctx)
	payment := gin.H{"tutorial_url": cfg.TutorialURL}
	if cfg.StaticQREnabled {
		payment["wechat_qr_url"] = h.qrURL(cfg.WechatQRFile)
		payment["alipay_qr_url"] = h.qrURL(cfg.AlipayQRFile)
	} else {
		payment["wechat_qr_url"] = ""
		payment["alipay_qr_url"] = ""
	}

	switch payCh {
	case "alipay_precreate":
		subBase := strings.TrimSpace(o.ProductTitleSnapshot)
		if subBase == "" {
			subBase = cfg.Title
		}
		subj := truncateSubject(subBase+" ×"+strconv.Itoa(o.Quantity), 256)
		totalStr := fmt.Sprintf("%.2f", centsToYuan(o.TotalCents))
		qr, perr := h.alipay.Precreate(h.notifyURL, o.ID.String(), subj, totalStr)
		if perr != nil {
			_ = h.store.DeleteClaudeAwaitingPaymentOrder(ctx, o.ID)
			c.JSON(http.StatusBadGateway, gin.H{"error": "支付宝下单失败: " + perr.Error()})
			return
		}
		payment["alipay_qr_code"] = qr
		payment["hint"] = "请使用支付宝扫描下方二维码（或保存截图扫码）。支付成功后系统将自动发货，无需管理员确认。"
	case "static":
		if !cfg.StaticPaymentManualConfirm {
			if ferr := h.store.FulfillClaudeOrder(ctx, o.ID); ferr != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": ferr.Error()})
				return
			}
			o, _ = h.store.GetClaudeOrderForAccount(ctx, o.ID, acc.ID)
			payment["hint"] = "已按店铺配置自动发货，请在「我的订单」查看账号信息。"
		} else {
			payment["hint"] = "请按订单应付金额扫描微信/支付宝静态收款码；支付完成后请等待管理员确认发货。"
		}
	}

	c.JSON(http.StatusCreated, gin.H{"order": o, "payment": payment})
}

// --- Admin ---

// GET /api/admin/shop/config
func (h *ClaudeShopHandler) AdminGetConfig(c *gin.Context) {
	cfg, err := h.store.GetClaudeShopConfig(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	summary, _ := h.store.GetClaudeInventorySummary(c.Request.Context())
	available := 0
	sold := 0
	total := 0
	if summary != nil {
		available = summary.Available
		sold = summary.Sold
		total = summary.Total
	}
	_, unassigned, _ := h.store.GetProductStockMap(c.Request.Context())
	c.JSON(http.StatusOK, gin.H{
		"enabled":                 cfg.Enabled,
		"title":                   cfg.Title,
		"subtitle":                cfg.Subtitle,
		"description":             cfg.Description,
		"tutorial_url":            cfg.TutorialURL,
		"retail_price_yuan":       centsToYuan(cfg.RetailPriceCents),
		"wholesale_min_qty":       cfg.WholesaleMinQty,
		"wholesale_price_yuan":    centsToYuan(cfg.WholesalePriceCents),
		"retail_price_cents":      cfg.RetailPriceCents,
		"wholesale_price_cents":   cfg.WholesalePriceCents,
		"tag_hot":                 cfg.TagHot,
		"show_tag_wholesale":      cfg.ShowTagWholesale,
		"tag_fan_welfare":         cfg.TagFanWelfare,
		"max_per_user":            cfg.MaxPerUser,
		"stock_available":         available,
		"stock_unassigned":        unassigned,
		"stock_sold":              sold,
		"stock_total":             total,
		"wechat_qr_url":           h.qrURL(cfg.WechatQRFile),
		"alipay_qr_url":           h.qrURL(cfg.AlipayQRFile),
		"static_payment_manual_confirm": cfg.StaticPaymentManualConfirm,
		"static_qr_enabled":             cfg.StaticQREnabled,
		"alipay_precreate_available":    h.alipay != nil && h.notifyURL != "" && h.appID != "",
	})
}

// PUT /api/admin/shop/config
func (h *ClaudeShopHandler) AdminPutConfig(c *gin.Context) {
	cur, err := h.store.GetClaudeShopConfig(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	var req struct {
		Enabled             *bool    `json:"enabled"`
		Title               *string  `json:"title"`
		Subtitle            *string  `json:"subtitle"`
		Description         *string  `json:"description"`
		TutorialURL         *string  `json:"tutorial_url"`
		RetailPriceYuan     *float64 `json:"retail_price_yuan"`
		WholesaleMinQty     *int     `json:"wholesale_min_qty"`
		WholesalePriceYuan  *float64 `json:"wholesale_price_yuan"`
		TagHot              *bool    `json:"tag_hot"`
		ShowTagWholesale    *bool    `json:"show_tag_wholesale"`
		TagFanWelfare       *string  `json:"tag_fan_welfare"`
		MaxPerUser          *int     `json:"max_per_user"`
		StaticPaymentManualConfirm *bool `json:"static_payment_manual_confirm"`
		StaticQREnabled            *bool `json:"static_qr_enabled"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if req.Enabled != nil {
		cur.Enabled = *req.Enabled
	}
	if req.Title != nil {
		cur.Title = strings.TrimSpace(*req.Title)
	}
	if req.Subtitle != nil {
		cur.Subtitle = strings.TrimSpace(*req.Subtitle)
	}
	if req.Description != nil {
		cur.Description = *req.Description
	}
	if req.TutorialURL != nil {
		cur.TutorialURL = strings.TrimSpace(*req.TutorialURL)
	}
	if req.RetailPriceYuan != nil {
		cur.RetailPriceCents = int(*req.RetailPriceYuan*100 + 0.5)
		if cur.RetailPriceCents < 0 {
			cur.RetailPriceCents = 0
		}
	}
	if req.WholesaleMinQty != nil {
		cur.WholesaleMinQty = *req.WholesaleMinQty
		if cur.WholesaleMinQty < 1 {
			cur.WholesaleMinQty = 1
		}
	}
	if req.WholesalePriceYuan != nil {
		cur.WholesalePriceCents = int(*req.WholesalePriceYuan*100 + 0.5)
		if cur.WholesalePriceCents < 0 {
			cur.WholesalePriceCents = 0
		}
	}
	if req.TagHot != nil {
		cur.TagHot = *req.TagHot
	}
	if req.ShowTagWholesale != nil {
		cur.ShowTagWholesale = *req.ShowTagWholesale
	}
	if req.TagFanWelfare != nil {
		cur.TagFanWelfare = strings.TrimSpace(*req.TagFanWelfare)
	}
	if req.MaxPerUser != nil {
		cur.MaxPerUser = *req.MaxPerUser
		if cur.MaxPerUser < 0 {
			cur.MaxPerUser = 0
		}
	}
	if req.StaticPaymentManualConfirm != nil {
		cur.StaticPaymentManualConfirm = *req.StaticPaymentManualConfirm
	}
	if req.StaticQREnabled != nil {
		cur.StaticQREnabled = *req.StaticQREnabled
	}
	if err := h.store.UpdateClaudeShopConfig(c.Request.Context(), cur); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "ok"})
}

// POST /api/admin/shop/qrcodes  multipart: wechat, alipay
func (h *ClaudeShopHandler) AdminUploadQR(c *gin.Context) {
	ctx := c.Request.Context()
	any := false
	updated := make([]string, 0, 2)
	if err := os.MkdirAll(h.assetDir, 0755); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	for _, field := range []string{"wechat", "alipay"} {
		fh, err := c.FormFile(field)
		if err != nil || fh == nil {
			continue
		}
		any = true
		ext := strings.ToLower(filepath.Ext(fh.Filename))
		if ext != ".png" && ext != ".jpg" && ext != ".jpeg" && ext != ".webp" && ext != ".gif" && ext != ".bmp" && ext != ".jfif" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "仅支持 png / jpg / jpeg / webp / gif / bmp / jfif"})
			return
		}
		if fh.Size > 8<<20 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "单张图片不超过 8MB"})
			return
		}
		safe := field + "_" + uuid.New().String() + ext
		dst := filepath.Join(h.assetDir, safe)
		if err := c.SaveUploadedFile(fh, dst); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		if err := h.store.SetClaudeShopQRFile(ctx, field, safe); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		updated = append(updated, field)
	}
	if !any {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请选择微信或支付宝收款码图片"})
		return
	}
	cfg, err := h.store.GetClaudeShopConfig(ctx)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"message": "upload ok", "updated": updated})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"message":        "upload ok",
		"updated":        updated,
		"wechat_qr_url":  h.qrURL(cfg.WechatQRFile),
		"alipay_qr_url":  h.qrURL(cfg.AlipayQRFile),
	})
}

// POST /api/admin/shop/inventory/import  Content-Type: text/plain 或 application/json
// v10：
//   - Content-Type: text/plain          → 传统卡密导入（自动识别 #### 分隔 / CSV）
//   - Content-Type: application/json    → 自定义发货导入；body 形如:
//     {
//       "delivery_type": "text" | "custom_kv",
//       "items": [ { "text": "..." } | { "url":"...","code":"..." } , ... ]
//     }
// Query: ?batch=xxx&product_id=<uuid>   product_id 留空 = 导入到通用池
func (h *ClaudeShopHandler) AdminImportInventory(c *gin.Context) {
	batch := strings.TrimSpace(c.Query("batch"))
	if len(batch) > 64 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "批次标识最长 64 字符"})
		return
	}
	if batch == "" {
		loc, err := time.LoadLocation("Asia/Shanghai")
		if err != nil {
			loc = time.UTC
		}
		batch = time.Now().In(loc).Format("0102")
	}
	var prodPtr *uuid.UUID
	var importProduct *model.ClaudeShopProduct
	if pidStr := strings.TrimSpace(c.Query("product_id")); pidStr != "" {
		pid, perr := parseUUID(pidStr)
		if perr != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "product_id 无效"})
			return
		}
		p, err := h.store.GetClaudeShopProductByID(c.Request.Context(), pid, false)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				c.JSON(http.StatusBadRequest, gin.H{"error": "商品不存在"})
				return
			}
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		importProduct = p
		prodPtr = &pid
	}

	contentType := strings.ToLower(c.ContentType())
	if strings.Contains(contentType, "application/json") {
		var req struct {
			DeliveryType string                   `json:"delivery_type"`
			Items        []map[string]interface{} `json:"items"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "JSON 解析失败: " + err.Error()})
			return
		}
		dt := strings.TrimSpace(req.DeliveryType)
		if dt != "text" && dt != "custom_kv" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "delivery_type 必须是 text 或 custom_kv"})
			return
		}
		if importProduct == nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "长文本 / 自定义字段库存请先选择对应 SKU，系统会按 SKU 的发货模式安全入库"})
			return
		}
		if importProduct.DeliveryType != dt {
			c.JSON(http.StatusBadRequest, gin.H{"error": "导入类型与所选 SKU 的发货模式不一致，请切换 SKU 或修改发货模式"})
			return
		}
		allowedCustomKeys := map[string]bool{}
		if dt == "custom_kv" {
			for _, f := range importProduct.DeliverySchema.Fields {
				k := strings.TrimSpace(f.Key)
				if k != "" {
					allowedCustomKeys[k] = true
				}
			}
			if len(allowedCustomKeys) == 0 {
				c.JSON(http.StatusBadRequest, gin.H{"error": "所选 SKU 还没有配置自定义发货字段，请先在商品与收款里配置字段"})
				return
			}
		}
		cleaned := make([]map[string]interface{}, 0, len(req.Items))
		for _, it := range req.Items {
			if len(it) == 0 {
				continue
			}
			// text 模式只保留 text 字段；custom_kv 原样透传
			if dt == "text" {
				if s, ok := it["text"].(string); ok && strings.TrimSpace(s) != "" {
					cleaned = append(cleaned, map[string]interface{}{"text": s})
				}
			} else {
				filtered := map[string]interface{}{}
				hasNonEmpty := false
				for k, v := range it {
					if !allowedCustomKeys[k] {
						continue
					}
					if s, ok := v.(string); ok {
						s = strings.TrimSpace(s)
						filtered[k] = s
						if s != "" {
							hasNonEmpty = true
						}
					} else if v != nil {
						filtered[k] = v
						hasNonEmpty = true
					}
				}
				if hasNonEmpty {
					cleaned = append(cleaned, filtered)
				}
			}
		}
		if len(cleaned) == 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "没有可导入的有效项"})
			return
		}
		n, err := h.store.ImportClaudeInventoryPayload(c.Request.Context(), cleaned, batch, prodPtr, dt)
		if err != nil {
			if err.Error() == "invalid_batch_label" {
				c.JSON(http.StatusBadRequest, gin.H{"error": "批次标识无效"})
				return
			}
			if err.Error() == "invalid_delivery_type" {
				c.JSON(http.StatusBadRequest, gin.H{"error": "发货模式无效"})
				return
			}
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		resp := gin.H{
			"delivery_type": dt,
			"recognized":    len(req.Items),
			"inserted":      n,
			"batch_label":   batch,
		}
		if prodPtr != nil {
			resp["product_id"] = prodPtr.String()
		}
		c.JSON(http.StatusOK, resp)
		return
	}

	// 默认路径：按卡密 text/plain 解析
	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "read body"})
		return
	}
	pairs, warns := store.ParseInventoryImport(string(body))
	if len(pairs) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "没有解析到有效行", "warnings": warns})
		return
	}
	if importProduct != nil && importProduct.DeliveryType != "card_key" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "所选 SKU 不是“邮箱+Key”发货模式，请在导入区使用对应的长文本 / 自定义字段表单"})
		return
	}
	n, err := h.store.ImportClaudeInventory(c.Request.Context(), pairs, batch, prodPtr)
	if err != nil {
		if err.Error() == "invalid_batch_label" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "批次标识无效"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	resp := gin.H{
		"delivery_type": "card_key",
		"recognized":    len(pairs),
		"inserted":      n,
		"skipped":       len(warns),
		"warnings":      warns,
		"batch_label":   batch,
	}
	if prodPtr != nil {
		resp["product_id"] = prodPtr.String()
	}
	c.JSON(http.StatusOK, resp)
}

// GET /api/admin/shop/inventory?status=available&page=1&size=30&product_id=<uuid|__none__>
func (h *ClaudeShopHandler) AdminListInventory(c *gin.Context) {
	status := c.DefaultQuery("status", "all")
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	size, _ := strconv.Atoi(c.DefaultQuery("size", "30"))
	if page < 1 {
		page = 1
	}
	if size < 1 || size > 100 {
		size = 30
	}
	batch := strings.TrimSpace(c.Query("batch"))
	product := strings.TrimSpace(c.Query("product_id"))
	list, total, err := h.store.ListClaudeInventory(c.Request.Context(), status, batch, product, page, size)
	if err != nil {
		switch err.Error() {
		case "invalid_inventory_status":
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid inventory status"})
			return
		case "invalid_product_filter":
			c.JSON(http.StatusBadRequest, gin.H{"error": "product_id 无效"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	summary, _ := h.store.GetClaudeInventorySummary(c.Request.Context())
	summaryResp := gin.H{"total": 0, "available": 0, "sold": 0}
	if summary != nil {
		summaryResp = gin.H{
			"total":     summary.Total,
			"available": summary.Available,
			"sold":      summary.Sold,
		}
	}
	c.JSON(http.StatusOK, gin.H{
		"data":    list,
		"total":   total,
		"page":    page,
		"size":    size,
		"summary": summaryResp,
	})
}

// GET /api/admin/shop/inventory/batches
func (h *ClaudeShopHandler) AdminListInventoryBatches(c *gin.Context) {
	list, unbatchedAvail, err := h.store.ListClaudeInventoryBatchSummaries(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": list, "unbatched_available": unbatchedAvail})
}

type purgeBatchBody struct {
	BatchLabel string `json:"batch_label" binding:"required"`
}

// POST /api/admin/shop/inventory/purge-batch 仅删除该批次下「待售」记录
func (h *ClaudeShopHandler) AdminPurgeInventoryBatch(c *gin.Context) {
	var req purgeBatchBody
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "JSON 需提供 batch_label（无批次待售用 __none__）"})
		return
	}
	key := strings.TrimSpace(req.BatchLabel)
	if key == "" || len(key) > 64 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "batch_label 无效"})
		return
	}
	n, err := h.store.PurgeClaudeInventoryBatchAvailable(c.Request.Context(), key)
	if err != nil {
		if err.Error() == "empty_batch" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "batch 无效"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"deleted": n})
}

// POST /api/admin/shop/inventory/purge-available 删除全部待售库存
func (h *ClaudeShopHandler) AdminPurgeAllAvailableInventory(c *gin.Context) {
	n, err := h.store.PurgeAllClaudeInventoryAvailable(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"deleted": n})
}

// DELETE /api/admin/shop/inventory/:id
func (h *ClaudeShopHandler) AdminDeleteInventory(c *gin.Context) {
	id, err := parseUUID(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid inventory id"})
		return
	}
	if err := h.store.DeleteClaudeInventory(c.Request.Context(), id); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			c.JSON(http.StatusNotFound, gin.H{"error": "inventory not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "deleted"})
}

// GET /api/admin/shop/orders?status=awaiting_payment&page=1&size=20
func (h *ClaudeShopHandler) AdminListOrders(c *gin.Context) {
	status := c.Query("status")
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	size, _ := strconv.Atoi(c.DefaultQuery("size", "20"))
	if page < 1 {
		page = 1
	}
	if size < 1 || size > 100 {
		size = 20
	}
	list, total, err := h.store.ListClaudeOrdersAdmin(c.Request.Context(), status, page, size)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": list, "total": total, "page": page, "size": size})
}

// GET /api/admin/shop/orders/:id 管理端订单详情（含发货后的 lines）
func (h *ClaudeShopHandler) AdminGetOrder(c *gin.Context) {
	id, err := parseUUID(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid order id"})
		return
	}
	o, err := h.store.GetClaudeOrderByID(c.Request.Context(), id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			c.JSON(http.StatusNotFound, gin.H{"error": "order not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"order": o})
}

// POST /api/admin/shop/orders/:id/confirm
func (h *ClaudeShopHandler) AdminConfirmOrder(c *gin.Context) {
	id, err := parseUUID(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid order id"})
		return
	}
	if err := h.store.FulfillClaudeOrder(c.Request.Context(), id); err != nil {
		if err.Error() == "insufficient_stock" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "库存不足，无法发货"})
			return
		}
		if err.Error() == "order_not_awaiting_payment" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "订单状态不是待确认收款"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	o, err := h.store.GetClaudeOrderByID(c.Request.Context(), id)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"message": "已发货"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "已发货", "order": o})
}

func cleanDeliverySchema(deliveryType string, schema model.DeliverySchema) (model.DeliverySchema, error) {
	if strings.TrimSpace(deliveryType) != "custom_kv" {
		return model.DeliverySchema{Fields: []model.DeliveryField{}}, nil
	}
	out := model.DeliverySchema{Fields: []model.DeliveryField{}}
	seen := map[string]bool{}
	for _, f := range schema.Fields {
		key := strings.TrimSpace(f.Key)
		label := strings.TrimSpace(f.Label)
		hint := strings.TrimSpace(f.Hint)
		if key == "" && label == "" {
			continue
		}
		if key == "" || strings.ContainsAny(key, " \t\r\n") || len(key) > 40 {
			return out, fmt.Errorf("invalid_delivery_schema_key")
		}
		if seen[key] {
			return out, fmt.Errorf("duplicate_delivery_schema_key")
		}
		seen[key] = true
		if label == "" {
			label = key
		}
		out.Fields = append(out.Fields, model.DeliveryField{Key: key, Label: label, Hint: hint, Multiline: f.Multiline})
	}
	if len(out.Fields) == 0 {
		return out, fmt.Errorf("delivery_schema_required")
	}
	return out, nil
}

// GET /api/admin/shop/products
func (h *ClaudeShopHandler) AdminListShopProducts(c *gin.Context) {
	ctx := c.Request.Context()
	list, err := h.store.ListClaudeShopProductsAdmin(ctx)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	stockMap, unassigned, _ := h.store.GetProductStockMap(ctx)
	out := make([]gin.H, 0, len(list))
	for i := range list {
		row := shopProductToResponse(&list[i])
		ps := stockMap[list[i].ID.String()]
		row["stock_dedicated"] = ps.Dedicated
		row["stock_with_unassigned"] = ps.WithUnassigned
		out = append(out, row)
	}
	c.JSON(http.StatusOK, gin.H{"data": out, "stock_unassigned": unassigned})
}

// POST /api/admin/shop/products
// v10 新增字段: delivery_type(card_key|text|custom_kv), delivery_schema, svip_price_yuan/svip_price_cents
func (h *ClaudeShopHandler) AdminCreateShopProduct(c *gin.Context) {
	var req struct {
		SortOrder          int      `json:"sort_order"`
		Enabled            *bool    `json:"enabled"`
		Title              string   `json:"title" binding:"required"`
		Description        string   `json:"description"`
		Tag                string   `json:"tag"`
		RetailPriceYuan    float64  `json:"retail_price_yuan"`
		WholesalePriceYuan float64  `json:"wholesale_price_yuan"`
		WholesaleMinQty    int      `json:"wholesale_min_qty"`
		DeliveryType       string   `json:"delivery_type"`
		DeliverySchema     *model.DeliverySchema `json:"delivery_schema"`
		SVIPPriceYuan      *float64 `json:"svip_price_yuan"`
		FixedContent       *string  `json:"fixed_content"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	title := strings.TrimSpace(req.Title)
	if title == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "标题不能为空"})
		return
	}
	en := true
	if req.Enabled != nil {
		en = *req.Enabled
	}
	wsq := req.WholesaleMinQty
	if wsq < 1 {
		wsq = 5
	}
	rp := int(req.RetailPriceYuan*100 + 0.5)
	wp := int(req.WholesalePriceYuan*100 + 0.5)
	if rp < 0 || wp < 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "价格无效"})
		return
	}
	var svipPrice *int
	if req.SVIPPriceYuan != nil {
		sp := int(*req.SVIPPriceYuan*100 + 0.5)
		if sp < 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "SVIP 价格无效"})
			return
		}
		svipPrice = &sp
	}
	schema := model.DeliverySchema{}
	if req.DeliverySchema != nil {
		schema = *req.DeliverySchema
	}
	deliveryType := strings.TrimSpace(req.DeliveryType)
	cleanSchema, err := cleanDeliverySchema(deliveryType, schema)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "自定义发货字段配置不完整：请至少保留 1 个不重复的字段 key"})
		return
	}
	fixedContent := ""
	if req.FixedContent != nil {
		fixedContent = *req.FixedContent
	}
	p := &model.ClaudeShopProduct{
		SortOrder:           req.SortOrder,
		Enabled:             en,
		Title:               title,
		Description:         req.Description,
		Tag:                 strings.TrimSpace(req.Tag),
		RetailPriceCents:    rp,
		WholesaleMinQty:     wsq,
		WholesalePriceCents: wp,
		DeliveryType:        deliveryType,
		DeliverySchema:      cleanSchema,
		SVIPPriceCents:      svipPrice,
		FixedContent:        fixedContent,
	}
	if err := h.store.InsertClaudeShopProduct(c.Request.Context(), p); err != nil {
		if err.Error() == "invalid_delivery_type" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "delivery_type 必须是 card_key / text / custom_kv"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, gin.H{"product": shopProductToResponse(p)})
}

// PUT /api/admin/shop/products/:id
func (h *ClaudeShopHandler) AdminUpdateShopProduct(c *gin.Context) {
	id, err := parseUUID(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}
	cur, err := h.store.GetClaudeShopProductByID(c.Request.Context(), id, false)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	var req struct {
		SortOrder          *int     `json:"sort_order"`
		Enabled            *bool    `json:"enabled"`
		Title              *string  `json:"title"`
		Description        *string  `json:"description"`
		Tag                *string  `json:"tag"`
		RetailPriceYuan    *float64 `json:"retail_price_yuan"`
		WholesalePriceYuan *float64 `json:"wholesale_price_yuan"`
		WholesaleMinQty    *int     `json:"wholesale_min_qty"`
		DeliveryType       *string  `json:"delivery_type"`
		DeliverySchema     *model.DeliverySchema `json:"delivery_schema"`
		SVIPPriceYuan      *float64 `json:"svip_price_yuan"`
		ClearSVIPPrice     *bool    `json:"clear_svip_price"`
		FixedContent       *string  `json:"fixed_content"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if req.FixedContent != nil {
		cur.FixedContent = *req.FixedContent
	}
	if req.SortOrder != nil {
		cur.SortOrder = *req.SortOrder
	}
	if req.Enabled != nil {
		cur.Enabled = *req.Enabled
	}
	if req.Title != nil {
		cur.Title = strings.TrimSpace(*req.Title)
	}
	if req.Description != nil {
		cur.Description = *req.Description
	}
	if req.Tag != nil {
		cur.Tag = strings.TrimSpace(*req.Tag)
	}
	if req.RetailPriceYuan != nil {
		cur.RetailPriceCents = int(*req.RetailPriceYuan*100 + 0.5)
	}
	if req.WholesalePriceYuan != nil {
		cur.WholesalePriceCents = int(*req.WholesalePriceYuan*100 + 0.5)
	}
	if req.WholesaleMinQty != nil {
		cur.WholesaleMinQty = *req.WholesaleMinQty
		if cur.WholesaleMinQty < 1 {
			cur.WholesaleMinQty = 1
		}
	}
	if req.DeliveryType != nil {
		cur.DeliveryType = strings.TrimSpace(*req.DeliveryType)
	}
	if req.DeliverySchema != nil {
		cur.DeliverySchema = *req.DeliverySchema
	}
	if req.ClearSVIPPrice != nil && *req.ClearSVIPPrice {
		cur.SVIPPriceCents = nil
	} else if req.SVIPPriceYuan != nil {
		sp := int(*req.SVIPPriceYuan*100 + 0.5)
		if sp < 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "SVIP 价格无效"})
			return
		}
		cur.SVIPPriceCents = &sp
	}
	if strings.TrimSpace(cur.Title) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "标题不能为空"})
		return
	}
	if cur.RetailPriceCents < 0 || cur.WholesalePriceCents < 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "价格无效"})
		return
	}
	cleanSchema, err := cleanDeliverySchema(cur.DeliveryType, cur.DeliverySchema)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "自定义发货字段配置不完整：请至少保留 1 个不重复的字段 key"})
		return
	}
	cur.DeliverySchema = cleanSchema
	if err := h.store.UpdateClaudeShopProduct(c.Request.Context(), cur); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
			return
		}
		if err.Error() == "invalid_delivery_type" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "delivery_type 必须是 card_key / text / custom_kv"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"product": shopProductToResponse(cur)})
}

// DELETE /api/admin/shop/products/:id
func (h *ClaudeShopHandler) AdminDeleteShopProduct(c *gin.Context) {
	id, err := parseUUID(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}
	if err := h.store.DeleteClaudeShopProduct(c.Request.Context(), id); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "ok"})
}
