package middleware

import (
	"net/http"
	"strings"

	"tempmail/model"
	"tempmail/store"

	"github.com/gin-gonic/gin"
)

const AccountKey = "account"

const ErrBannedMessage = "由于违反邮箱服务协议，您的账户已被封禁，无法登录。"

// Auth API Key 认证中间件
func Auth(s *store.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		apiKey := c.GetHeader("Authorization")
		if apiKey == "" {
			apiKey = c.Query("api_key")
		}

		apiKey = strings.TrimPrefix(apiKey, "Bearer ")
		apiKey = strings.TrimSpace(apiKey)

		if apiKey == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"error": "missing api_key: use Authorization header or ?api_key= query param",
			})
			return
		}

		account, err := s.GetAccountByAPIKey(c.Request.Context(), apiKey)
		if err == nil && account != nil {
			_ = s.TouchAccountLastSeen(c.Request.Context(), account.ID)
			c.Set(AccountKey, account)
			c.Next()
			return
		}

		// 区分：Key 存在但已停用（封禁） vs 无效 Key
		anyAcc, errAny := s.GetAccountByAPIKeyAny(c.Request.Context(), apiKey)
		if errAny == nil && anyAcc != nil && !anyAcc.IsActive {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
				"error": ErrBannedMessage,
				"code":  "account_banned",
			})
			return
		}

		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
			"error": "invalid api_key",
		})
	}
}

// AdminOnly 管理员权限中间件
func AdminOnly() gin.HandlerFunc {
	return func(c *gin.Context) {
		account := GetAccount(c)
		if account == nil || !account.IsAdmin {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
				"error": "admin access required",
			})
			return
		}
		c.Next()
	}
}

func GetAccount(c *gin.Context) *model.Account {
	val, exists := c.Get(AccountKey)
	if !exists {
		return nil
	}
	a, ok := val.(*model.Account)
	if !ok {
		return nil
	}
	return a
}
