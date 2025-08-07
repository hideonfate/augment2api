package middleware

import (
	"augment2api/config"
	"augment2api/pkg/logger"
	tokenmanager "augment2api/pkg/token"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"
)



// TokenConcurrencyMiddleware 控制Redis中token的使用频率
func TokenConcurrencyMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		// 只对聊天完成请求和消息请求进行并发控制
		if !strings.HasSuffix(c.Request.URL.Path, "/chat/completions") && !strings.HasSuffix(c.Request.URL.Path, "/messages") {
			c.Next()
			return
		}

		// 调试模式无需限制
		if config.AppConfig.CodingMode == "true" {
			token := config.AppConfig.CodingToken
			tenantURL := config.AppConfig.TenantURL
			c.Set("token", token)
			c.Set("tenant_url", tenantURL)
			c.Next()
			return
		}

		// 获取一个可用的token
		tokenStr, tenantURL, sessionID := tokenmanager.GetAvailableToken()
		if tokenStr == "No token" {
			c.JSON(http.StatusTooManyRequests, gin.H{"error": "当前无可用token，请在页面添加"})
			c.Abort()
			return
		}
		if tokenStr == "No available token" || tenantURL == "" {
			c.JSON(http.StatusTooManyRequests, gin.H{"error": "当前请求过多，请稍后再试"})
			c.Abort()
			return
		}

		// 获取该token的锁
		lock := tokenmanager.GetTokenLock(tokenStr)

		// 尝试获取锁，会阻塞直到获取到锁
		lock.Lock()

		// 更新请求状态
		err := tokenmanager.SetTokenRequestStatus(tokenStr, tokenmanager.TokenRequestStatus{
			InProgress:    true,
			LastRequestAt: time.Now(),
		})

		if err != nil {
			lock.Unlock()
			c.JSON(http.StatusInternalServerError, gin.H{"error": "更新token请求状态失败"})
			c.Abort()
			return
		}

		logger.Log.WithFields(logrus.Fields{
			"token":      tokenStr,
			"session_id": sessionID,
		}).Info("本次请求使用的token: ")

		// 在请求完成后释放锁
		c.Set("token_lock", lock)
		c.Set("token", tokenStr)
		c.Set("tenant_url", tenantURL)
		c.Set("session_id", sessionID)

		c.Next()
	}
}

// SwitchTokenAndRetry 当遇到429错误时切换Token并重试
func SwitchTokenAndRetry(c *gin.Context, maxRetries int) bool {
	return tokenmanager.SwitchTokenAndRetry(c, maxRetries)
}
