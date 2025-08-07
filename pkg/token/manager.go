package token

import (
	"augment2api/config"
	"augment2api/pkg/logger"
	"encoding/json"
	"math/rand"
	"strconv"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/sirupsen/logrus"
)

// 全局锁映射，用于控制每个 token 的并发请求
var (
	tokenLocks      = make(map[string]*sync.Mutex)
	tokenLocksGuard = sync.Mutex{}
)

// TokenRequestStatus 记录 token 请求状态
type TokenRequestStatus struct {
	InProgress    bool      `json:"in_progress"`
	LastRequestAt time.Time `json:"last_request_at"`
}

// TokenCoolStatus 记录 token 冷却状态
type TokenCoolStatus struct {
	InCool  bool      `json:"in_cool"`
	CoolEnd time.Time `json:"cool_end"`
}

// GetTokenLock 获取指定 token 的锁
func GetTokenLock(token string) *sync.Mutex {
	tokenLocksGuard.Lock()
	defer tokenLocksGuard.Unlock()

	if lock, exists := tokenLocks[token]; exists {
		return lock
	}

	lock := &sync.Mutex{}
	tokenLocks[token] = lock
	return lock
}

// SetTokenRequestStatus 设置token请求状态
func SetTokenRequestStatus(token string, status TokenRequestStatus) error {
	// 使用Redis存储token请求状态
	key := "token_status:" + token

	// 将状态转换为JSON
	statusJSON, err := json.Marshal(status)
	if err != nil {
		return err
	}

	// 存储到Redis，设置过期时间为1小时
	return config.RedisSet(key, string(statusJSON), time.Hour)
}

// GetTokenRequestStatus 获取token请求状态
func GetTokenRequestStatus(token string) (TokenRequestStatus, error) {
	key := "token_status:" + token

	// 从Redis获取状态
	statusJSON, err := config.RedisGet(key)
	if err != nil {
		// 如果不存在，返回默认状态
		return TokenRequestStatus{
			InProgress:    false,
			LastRequestAt: time.Time{},
		}, nil
	}

	var status TokenRequestStatus
	err = json.Unmarshal([]byte(statusJSON), &status)
	if err != nil {
		return TokenRequestStatus{}, err
	}

	return status, nil
}

// SetTokenCoolStatus 将token加入冷却队列
func SetTokenCoolStatus(token string, duration time.Duration) error {
	// 使用Redis存储token冷却状态
	key := "token_cool_status:" + token

	coolStatus := TokenCoolStatus{
		InCool:  true,
		CoolEnd: time.Now().Add(duration),
	}

	// 将状态转换为JSON
	coolStatusJSON, err := json.Marshal(coolStatus)
	if err != nil {
		return err
	}

	// 存储到Redis，设置过期时间与冷却时间相同
	return config.RedisSet(key, string(coolStatusJSON), duration)
}

// GetTokenCoolStatus 获取token冷却状态
func GetTokenCoolStatus(token string) (TokenCoolStatus, error) {
	key := "token_cool_status:" + token

	// 从Redis获取状态
	coolStatusJSON, err := config.RedisGet(key)
	if err != nil {
		// 如果不存在，返回默认状态（不在冷却中）
		return TokenCoolStatus{
			InCool:  false,
			CoolEnd: time.Time{},
		}, nil
	}

	var coolStatus TokenCoolStatus
	err = json.Unmarshal([]byte(coolStatusJSON), &coolStatus)
	if err != nil {
		return TokenCoolStatus{}, err
	}

	// 检查冷却是否已过期
	if coolStatus.InCool && time.Now().After(coolStatus.CoolEnd) {
		coolStatus.InCool = false
	}

	return coolStatus, nil
}

// getTokenChatUsageCount 获取token的CHAT模式使用次数
func getTokenChatUsageCount(token string) int {
	// 使用Redis中的计数器获取使用次数
	countKey := "token_usage_chat:" + token
	count, err := config.RedisGet(countKey)
	if err != nil {
		return 0 // 如果出错或不存在，返回0
	}

	countInt, _ := strconv.Atoi(count)

	return countInt
}

// getTokenAgentUsageCount 获取token的AGENT模式使用次数
func getTokenAgentUsageCount(token string) int {
	// 使用Redis中的计数器获取使用次数
	countKey := "token_usage_agent:" + token
	count, err := config.RedisGet(countKey)
	if err != nil {
		return 0 // 如果出错或不存在，返回0
	}

	countInt, _ := strconv.Atoi(count)

	return countInt
}

// GetAvailableToken 获取一个可用的token（未在使用中且冷却时间已过），同时返回token、tenant_url和session_id
func GetAvailableToken() (string, string, string) {
	// 获取所有token的key
	keys, err := config.RedisKeys("token:*")
	if err != nil || len(keys) == 0 {
		return "No token", "", ""
	}

	// 筛选可用的token
	var availableTokens []string
	var availableTenantURLs []string
	var availableSessionIDs []string
	var cooldownTokens []string
	var cooldownTenantURLs []string
	var cooldownSessionIDs []string

	for _, key := range keys {
		// 获取token状态
		status, err := config.RedisHGet(key, "status")
		if err == nil && status == "disabled" {
			continue // 跳过被标记为不可用的token
		}

		// 从key中提取token
		token := key[6:] // 去掉前缀 "token:"

		// 获取token的请求状态
		requestStatus, err := GetTokenRequestStatus(token)
		if err != nil {
			continue
		}

		// 如果token正在使用中，跳过
		if requestStatus.InProgress {
			continue
		}

		// 如果距离上次请求不足3秒，跳过
		if time.Since(requestStatus.LastRequestAt) < 3*time.Second {
			continue
		}

		// 检查CHAT模式和AGENT模式的使用次数限制
		chatUsageCount := getTokenChatUsageCount(token)
		agentUsageCount := getTokenAgentUsageCount(token)

		// 如果CHAT模式已达到3000次限制，跳过
		if chatUsageCount >= 3000 {
			continue
		}

		// 如果AGENT模式已达到50次限制，跳过
		if agentUsageCount >= 50 {
			continue
		}

		// 获取对应的tenant_url
		tenantURL, err := config.RedisHGet(key, "tenant_url")
		if err != nil {
			continue
		}

		// 获取对应的session_id
		sessionID, err := config.RedisHGet(key, "session_id")
		if err != nil {
			// 如果没有session_id，生成一个新的
			sessionID = uuid.New().String()
			config.RedisHSet(key, "session_id", sessionID)
		}

		// 检查token是否在冷却中
		coolStatus, err := GetTokenCoolStatus(token)
		if err != nil {
			continue
		}

		// 如果token在冷却中，放入冷却队列
		if coolStatus.InCool {
			cooldownTokens = append(cooldownTokens, token)
			cooldownTenantURLs = append(cooldownTenantURLs, tenantURL)
			cooldownSessionIDs = append(cooldownSessionIDs, sessionID)
		} else {
			// 否则放入可用队列
			availableTokens = append(availableTokens, token)
			availableTenantURLs = append(availableTenantURLs, tenantURL)
			availableSessionIDs = append(availableSessionIDs, sessionID)
		}
	}

	// 优先从可用队列中选择token
	if len(availableTokens) > 0 {
		// 随机选择一个token
		randomIndex := rand.Intn(len(availableTokens))
		return availableTokens[randomIndex], availableTenantURLs[randomIndex], availableSessionIDs[randomIndex]
	}

	// 如果没有非冷却token可用，则从冷却队列中选择
	if len(cooldownTokens) > 0 {
		// 随机选择一个token
		randomIndex := rand.Intn(len(cooldownTokens))
		return cooldownTokens[randomIndex], cooldownTenantURLs[randomIndex], cooldownSessionIDs[randomIndex]
	}

	// 如果没有任何可用的token
	return "No available token", "", ""
}

// GetNextAvailableToken 获取下一个可用的token（排除指定token），用于重试机制
func GetNextAvailableToken(excludeToken string) (string, string, string) {
	// 获取所有token的key
	keys, err := config.RedisKeys("token:*")
	if err != nil || len(keys) == 0 {
		return "No token", "", ""
	}

	// 筛选可用的token（排除指定的token）
	var availableTokens []string
	var availableTenantURLs []string
	var availableSessionIDs []string
	var cooldownTokens []string
	var cooldownTenantURLs []string
	var cooldownSessionIDs []string

	for _, key := range keys {
		// 获取token状态
		status, err := config.RedisHGet(key, "status")
		if err == nil && status == "disabled" {
			continue // 跳过被标记为不可用的token
		}

		// 从key中提取token
		token := key[6:] // 去掉前缀 "token:"

		// 排除指定的token
		if token == excludeToken {
			continue
		}

		// 获取token的请求状态
		requestStatus, err := GetTokenRequestStatus(token)
		if err != nil {
			continue
		}

		// 如果token正在使用中，跳过
		if requestStatus.InProgress {
			continue
		}

		// 如果距离上次请求不足3秒，跳过
		if time.Since(requestStatus.LastRequestAt) < 3*time.Second {
			continue
		}

		// 检查CHAT模式和AGENT模式的使用次数限制
		chatUsageCount := getTokenChatUsageCount(token)
		agentUsageCount := getTokenAgentUsageCount(token)

		// 如果CHAT模式已达到3000次限制，跳过
		if chatUsageCount >= 3000 {
			continue
		}

		// 如果AGENT模式已达到50次限制，跳过
		if agentUsageCount >= 50 {
			continue
		}

		// 获取对应的tenant_url
		tenantURL, err := config.RedisHGet(key, "tenant_url")
		if err != nil {
			continue
		}

		// 获取对应的session_id
		sessionID, err := config.RedisHGet(key, "session_id")
		if err != nil {
			// 如果没有session_id，生成一个新的
			sessionID = uuid.New().String()
			config.RedisHSet(key, "session_id", sessionID)
		}

		// 检查token是否在冷却中
		coolStatus, err := GetTokenCoolStatus(token)
		if err != nil {
			continue
		}

		// 如果token在冷却中，放入冷却队列
		if coolStatus.InCool {
			cooldownTokens = append(cooldownTokens, token)
			cooldownTenantURLs = append(cooldownTenantURLs, tenantURL)
			cooldownSessionIDs = append(cooldownSessionIDs, sessionID)
		} else {
			// 否则放入可用队列
			availableTokens = append(availableTokens, token)
			availableTenantURLs = append(availableTenantURLs, tenantURL)
			availableSessionIDs = append(availableSessionIDs, sessionID)
		}
	}

	// 优先从可用队列中选择token
	if len(availableTokens) > 0 {
		// 随机选择一个token
		randomIndex := rand.Intn(len(availableTokens))
		return availableTokens[randomIndex], availableTenantURLs[randomIndex], availableSessionIDs[randomIndex]
	}

	// 如果没有非冷却token可用，则从冷却队列中选择
	if len(cooldownTokens) > 0 {
		// 随机选择一个token
		randomIndex := rand.Intn(len(cooldownTokens))
		return cooldownTokens[randomIndex], cooldownTenantURLs[randomIndex], cooldownSessionIDs[randomIndex]
	}

	// 如果没有任何可用的token
	return "No available token", "", ""
}

// SwitchTokenAndRetry 当遇到429错误时切换Token并重试
func SwitchTokenAndRetry(c *gin.Context, maxRetries int) bool {
	// 获取当前Token
	currentTokenInterface, exists := c.Get("token")
	if !exists {
		return false
	}
	currentToken, ok := currentTokenInterface.(string)
	if !ok {
		return false
	}

	// 获取重试次数
	retryCountInterface, exists := c.Get("retry_count")
	retryCount := 0
	if exists {
		retryCount, _ = retryCountInterface.(int)
	}

	// 检查是否超过最大重试次数
	if retryCount >= maxRetries {
		logger.Log.WithFields(logrus.Fields{
			"current_token": currentToken,
			"retry_count":   retryCount,
		}).Warn("已达到最大重试次数，停止重试")
		return false
	}

	// 将当前Token加入短期冷却（5分钟）
	err := SetTokenCoolStatus(currentToken, 5*time.Minute)
	if err != nil {
		logger.Log.WithFields(logrus.Fields{
			"token": currentToken,
			"error": err.Error(),
		}).Error("设置Token冷却状态失败")
	} else {
		logger.Log.WithFields(logrus.Fields{
			"token": currentToken,
		}).Info("Token因429错误被加入5分钟冷却")
	}

	// 获取下一个可用Token
	nextToken, nextTenantURL, nextSessionID := GetNextAvailableToken(currentToken)
	if nextToken == "No token" || nextToken == "No available token" {
		logger.Log.WithFields(logrus.Fields{
			"current_token": currentToken,
		}).Warn("没有其他可用Token进行重试")
		return false
	}

	// 释放当前Token的锁
	currentLockInterface, exists := c.Get("token_lock")
	if exists {
		if currentLock, ok := currentLockInterface.(*sync.Mutex); ok {
			// 更新当前Token的请求状态为已完成
			SetTokenRequestStatus(currentToken, TokenRequestStatus{
				InProgress:    false,
				LastRequestAt: time.Now(),
			})
			currentLock.Unlock()
		}
	}

	// 获取新Token的锁
	newLock := GetTokenLock(nextToken)
	newLock.Lock()

	// 更新新Token的请求状态
	err = SetTokenRequestStatus(nextToken, TokenRequestStatus{
		InProgress:    true,
		LastRequestAt: time.Now(),
	})
	if err != nil {
		newLock.Unlock()
		logger.Log.WithFields(logrus.Fields{
			"token": nextToken,
			"error": err.Error(),
		}).Error("更新新Token请求状态失败")
		return false
	}

	// 更新Context中的Token信息
	c.Set("token", nextToken)
	c.Set("tenant_url", nextTenantURL)
	c.Set("session_id", nextSessionID)
	c.Set("token_lock", newLock)
	c.Set("retry_count", retryCount+1)

	logger.Log.WithFields(logrus.Fields{
		"old_token":   currentToken,
		"new_token":   nextToken,
		"retry_count": retryCount + 1,
	}).Info("Token切换成功，准备重试")

	return true
}
