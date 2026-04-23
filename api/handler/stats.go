package handler

import (
	"net/http"
	"strconv"

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

// GET /api/admin/stats/api-calls?days=7&page=1&size=50
func (h *StatsHandler) APICallStats(c *gin.Context) {
	days, _ := strconv.Atoi(c.DefaultQuery("days", "7"))
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	size, _ := strconv.Atoi(c.DefaultQuery("size", "50"))
	if page < 1 {
		page = 1
	}
	if size < 1 || size > 200 {
		size = 50
	}
	list, total, err := h.store.ListAPICallStats(c.Request.Context(), days, page, size)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": list, "total": total, "page": page, "size": size, "days": days})
}

// GET /api/admin/stats/top-callers?days=7&limit=20
func (h *StatsHandler) TopCallers(c *gin.Context) {
	days, _ := strconv.Atoi(c.DefaultQuery("days", "7"))
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))
	list, err := h.store.TopAPICallers(c.Request.Context(), days, limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": list, "days": days})
}
