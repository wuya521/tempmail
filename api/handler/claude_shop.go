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
		"alipay_precreate_available": h.alipay != nil && h.notifyURL != "" && h.appID != "",
		"static_payment_manual_confirm": cfg.StaticPaymentManualConfirm,
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
	pay := gin.H{
		"wechat_qr_url": h.qrURL(cfg.WechatQRFile),
		"alipay_qr_url": h.qrURL(cfg.AlipayQRFile),
		"tutorial_url":  cfg.TutorialURL,
	}
	if o.Status == "awaiting_payment" && o.PaymentChannel == "alipay_precreate" {
		pay["hint"] = "本单为支付宝当面付：若二维码已过期，请稍后在订单页刷新或联系管理员。"
	}
	c.JSON(http.StatusOK, gin.H{"order": o, "payment": pay})
}

// POST /api/shop/orders  body: { "quantity": n, "payment_method": "static" | "alipay" }
func (h *ClaudeShopHandler) CreateOrder(c *gin.Context) {
	acc := middleware.GetAccount(c)
	var req struct {
		Quantity       int    `json:"quantity" binding:"required,min=1,max=999"`
		PaymentMethod  string `json:"payment_method"` // 默认 static；alipay 表示当面付 precreate
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
	o, err := h.store.CreateClaudeOrder(ctx, acc.ID, req.Quantity, payCh)
	if err != nil {
		switch err.Error() {
		case "shop_disabled":
			c.JSON(http.StatusForbidden, gin.H{"error": "自助购号已关闭"})
		case "insufficient_stock":
			c.JSON(http.StatusBadRequest, gin.H{"error": "库存不足"})
		case "invalid_quantity":
			c.JSON(http.StatusBadRequest, gin.H{"error": "数量无效"})
		case "invalid_payment_channel":
			c.JSON(http.StatusBadRequest, gin.H{"error": "无效的支付渠道"})
		case "exceeds_purchase_limit":
			c.JSON(http.StatusBadRequest, gin.H{"error": "超过每用户限购数量"})
		case "pending_order_exists":
			c.JSON(http.StatusConflict, gin.H{"error": "您已有待支付/待确认的订单，请在「我的购买记录」中查看，勿重复提交"})
		default:
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		}
		return
	}

	cfg, _ := h.store.GetClaudeShopConfig(ctx)
	payment := gin.H{
		"wechat_qr_url": h.qrURL(cfg.WechatQRFile),
		"alipay_qr_url": h.qrURL(cfg.AlipayQRFile),
		"tutorial_url":  cfg.TutorialURL,
	}

	switch payCh {
	case "alipay_precreate":
		subj := truncateSubject(cfg.Title+" ×"+strconv.Itoa(o.Quantity), 256)
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
		"stock_sold":              sold,
		"stock_total":             total,
		"wechat_qr_url":           h.qrURL(cfg.WechatQRFile),
		"alipay_qr_url":           h.qrURL(cfg.AlipayQRFile),
		"static_payment_manual_confirm": cfg.StaticPaymentManualConfirm,
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
	n, err := h.store.ImportClaudeInventory(c.Request.Context(), pairs, batch)
	if err != nil {
		if err.Error() == "invalid_batch_label" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "批次标识无效"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"recognized":   len(pairs),
		"inserted":     n,
		"skipped":      len(warns),
		"warnings":     warns,
		"batch_label":  batch,
	})
}

// GET /api/admin/shop/inventory?status=available&page=1&size=30
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
	list, total, err := h.store.ListClaudeInventory(c.Request.Context(), status, batch, page, size)
	if err != nil {
		if err.Error() == "invalid_inventory_status" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid inventory status"})
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
