package handler

import (
	"errors"
	"io"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"

	"tempmail/middleware"
	"tempmail/store"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type ClaudeShopHandler struct {
	store     *store.Store
	assetDir  string
	publicURL string // 如 /public/shop-assets
}

func NewClaudeShopHandler(s *store.Store, assetDir string) *ClaudeShopHandler {
	return &ClaudeShopHandler{
		store:     s,
		assetDir:  assetDir,
		publicURL: "/public/shop-assets",
	}
}

func centsToYuan(c int) float64 {
	return float64(c) / 100
}

// GET /public/claude-shop
func (h *ClaudeShopHandler) PublicSummary(c *gin.Context) {
	cfg, err := h.store.GetClaudeShopConfig(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"enabled": false, "error": "shop not initialized"})
		return
	}
	stock, _ := h.store.CountClaudeInventoryAvailable(c.Request.Context())
	out := gin.H{
		"enabled":                 cfg.Enabled,
		"title":                   cfg.Title,
		"subtitle":                cfg.Subtitle,
		"description":             cfg.Description,
		"tutorial_url":            cfg.TutorialURL,
		"retail_price_yuan":       centsToYuan(cfg.RetailPriceCents),
		"wholesale_min_qty":       cfg.WholesaleMinQty,
		"wholesale_price_yuan":    centsToYuan(cfg.WholesalePriceCents),
		"tag_hot":                 cfg.TagHot,
		"show_tag_wholesale":      cfg.ShowTagWholesale,
		"tag_fan_welfare":         cfg.TagFanWelfare,
		"max_per_user":            cfg.MaxPerUser,
		"stock_available":         stock,
		"wechat_qr_url":           h.qrURL(cfg.WechatQRFile),
		"alipay_qr_url":           h.qrURL(cfg.AlipayQRFile),
		"retail_price_cents":      cfg.RetailPriceCents,
		"wholesale_price_cents":   cfg.WholesalePriceCents,
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
	if name != cfg.WechatQRFile && name != cfg.AlipayQRFile {
		c.Status(http.StatusNotFound)
		return
	}
	c.File(filepath.Join(h.assetDir, name))
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
	c.JSON(http.StatusOK, gin.H{
		"order": o,
		"payment": gin.H{
			"wechat_qr_url": h.qrURL(cfg.WechatQRFile),
			"alipay_qr_url": h.qrURL(cfg.AlipayQRFile),
			"tutorial_url":  cfg.TutorialURL,
		},
	})
}

// POST /api/shop/orders  body: { "quantity": n }
func (h *ClaudeShopHandler) CreateOrder(c *gin.Context) {
	acc := middleware.GetAccount(c)
	var req struct {
		Quantity int `json:"quantity" binding:"required,min=1,max=999"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	o, err := h.store.CreateClaudeOrder(c.Request.Context(), acc.ID, req.Quantity)
	if err != nil {
		switch err.Error() {
		case "shop_disabled":
			c.JSON(http.StatusForbidden, gin.H{"error": "自助购号已关闭"})
		case "insufficient_stock":
			c.JSON(http.StatusBadRequest, gin.H{"error": "库存不足"})
		case "invalid_quantity":
			c.JSON(http.StatusBadRequest, gin.H{"error": "数量无效"})
		case "exceeds_purchase_limit":
			c.JSON(http.StatusBadRequest, gin.H{"error": "超过每用户限购数量"})
		default:
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		}
		return
	}
	cfg, _ := h.store.GetClaudeShopConfig(c.Request.Context())
	c.JSON(http.StatusCreated, gin.H{
		"order": o,
		"payment": gin.H{
			"wechat_qr_url": h.qrURL(cfg.WechatQRFile),
			"alipay_qr_url": h.qrURL(cfg.AlipayQRFile),
			"tutorial_url":  cfg.TutorialURL,
			"hint":          "请按订单应付金额扫码支付，支付完成后请等待管理员确认发货。",
		},
	})
}

// --- Admin ---

// GET /api/admin/shop/config
func (h *ClaudeShopHandler) AdminGetConfig(c *gin.Context) {
	cfg, err := h.store.GetClaudeShopConfig(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	stock, _ := h.store.CountClaudeInventoryAvailable(c.Request.Context())
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
		"stock_available":         stock,
		"wechat_qr_url":           h.qrURL(cfg.WechatQRFile),
		"alipay_qr_url":           h.qrURL(cfg.AlipayQRFile),
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
	for _, field := range []string{"wechat", "alipay"} {
		fh, err := c.FormFile(field)
		if err != nil || fh == nil {
			continue
		}
		any = true
		ext := strings.ToLower(filepath.Ext(fh.Filename))
		if ext != ".png" && ext != ".jpg" && ext != ".jpeg" && ext != ".webp" && ext != ".gif" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "仅支持 png / jpg / jpeg / webp / gif"})
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
	}
	if !any {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请选择微信或支付宝收款码图片"})
		return
	}
	h.AdminGetConfig(c)
}

// POST /api/admin/shop/inventory/import  Content-Type: text/plain
func (h *ClaudeShopHandler) AdminImportInventory(c *gin.Context) {
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
	n, err := h.store.ImportClaudeInventory(c.Request.Context(), pairs)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"inserted": n, "warnings": warns})
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

