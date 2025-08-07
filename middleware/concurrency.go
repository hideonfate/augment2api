package middleware

import (
	"augment2api/api"
	"augment2api/config"
	"augment2api/pkg/logger"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"
)

// 全局锁映射，用于控制每个 token 的并发请求
var (
	tokenLocks      = make(map[string]*sync.Mutex)
	tokenLocksGuard = sync.Mutex{}
)

// getTokenLock 获取指定 token 的锁
func getTokenLock(token string) *sync.Mutex {
	tokenLocksGuard.Lock()
	defer tokenLocksGuard.Unlock()

	if lock, exists := tokenLocks[token]; exists {
		return lock
	}

	lock := &sync.Mutex{}
	tokenLocks[token] = lock
	return lock
}

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
		token, tenantURL, sessionID := api.GetAvailableToken()
		if token == "No token" {
			c.JSON(http.StatusTooManyRequests, gin.H{"error": "当前无可用token，请在页面添加"})
			c.Abort()
			return
		}
		if token == "No available token" || tenantURL == "" {
			c.JSON(http.StatusTooManyRequests, gin.H{"error": "当前请求过多，请稍后再试"})
			c.Abort()
			return
		}

		// 获取该token的锁
		lock := getTokenLock(token)

		// 尝试获取锁，会阻塞直到获取到锁
		lock.Lock()

		// 更新请求状态
		err := api.SetTokenRequestStatus(token, api.TokenRequestStatus{
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
			"token":      token,
			"session_id": sessionID,
		}).Info("本次请求使用的token: ")

		// 在请求完成后释放锁
		c.Set("token_lock", lock)
		c.Set("token", token)
		c.Set("tenant_url", tenantURL)
		c.Set("session_id", sessionID)

		c.Next()
	}
}
