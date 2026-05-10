package handler

import (
	"net/http"

	"tempmail/store"

	"github.com/gin-gonic/gin"
)

type StatsHandler struct {
	store *store.Store
}

func NewStatsHandler(s *store.Store) *StatsHandler {
	return &StatsHandler{store: s}
}

// GET /public/stats  — 公开统计（无需认证）
// GET /api/stats     — 同上（认证后可调用）
func (h *StatsHandler) Get(c *gin.Context) {
	stats, err := h.store.GetStats(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, stats)
}

// GET /api/admin/dashboard — 管理员数据仪表盘
func (h *StatsHandler) AdminDashboard(c *gin.Context) {
	ctx := c.Request.Context()
	dash, err := h.store.GetDashboardStats(ctx)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	recent, _ := h.store.GetRecentUsers(ctx, 10)
	c.JSON(http.StatusOK, gin.H{
		"stats":        dash,
		"recent_users": recent,
	})
}
