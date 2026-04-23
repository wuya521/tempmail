package middleware

import (
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
)

// RateLimit 基于 Redis 的滑动窗口速率限制
// limit: 每个窗口允许的请求数
// window: 窗口大小（秒）
func RateLimit(rdb *redis.Client, limit int, window int) gin.HandlerFunc {
	windowDur := time.Duration(window) * time.Second

	return func(c *gin.Context) {
		// 使用 API Key 作为限速键
		key := c.GetHeader("Authorization")
		if key == "" {
			key = c.Query("api_key")
		}
		if key == "" {
			key = c.ClientIP()
		}

		redisKey := fmt.Sprintf("rl:%s", key)
		ctx := c.Request.Context()

		// 使用 Redis Pipeline 减少往返（高并发优化）
		pipe := rdb.Pipeline()
		incr := pipe.Incr(ctx, redisKey)
		pipe.Expire(ctx, redisKey, windowDur)
		_, err := pipe.Exec(ctx)

		if err != nil {
			// Redis 故障时放行（fail-open）
			c.Next()
			return
		}

		count := incr.Val()
		remaining := int64(limit) - count
		if remaining < 0 {
			remaining = 0
		}

		c.Header("X-RateLimit-Limit", fmt.Sprintf("%d", limit))
		c.Header("X-RateLimit-Remaining", fmt.Sprintf("%d", remaining))
		c.Header("X-RateLimit-Reset", fmt.Sprintf("%d", time.Now().Add(windowDur).Unix()))

		if count > int64(limit) {
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{
				"error":       "rate limit exceeded",
				"limit":       limit,
				"retry_after": window,
			})
			return
		}

		c.Next()
	}
}
