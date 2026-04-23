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
		key := c.GetHeader("Authorization")
		if key == "" {
			key = c.Query("api_key")
		}
		if key == "" {
			key = c.ClientIP()
		}

		redisKey := fmt.Sprintf("rl:%s", key)
		ctx := c.Request.Context()

		count, err := rdb.Incr(ctx, redisKey).Result()
		if err != nil {
			c.Next()
			return
		}
		if count == 1 {
			rdb.Expire(ctx, redisKey, windowDur)
		}

		remaining := int64(limit) - count
		if remaining < 0 {
			remaining = 0
		}

		ttl, _ := rdb.TTL(ctx, redisKey).Result()
		resetAt := time.Now().Add(ttl).Unix()
		if ttl <= 0 {
			resetAt = time.Now().Add(windowDur).Unix()
		}

		c.Header("X-RateLimit-Limit", fmt.Sprintf("%d", limit))
		c.Header("X-RateLimit-Remaining", fmt.Sprintf("%d", remaining))
		c.Header("X-RateLimit-Reset", fmt.Sprintf("%d", resetAt))

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
