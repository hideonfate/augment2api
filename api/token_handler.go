package api

import (
	"augment2api/config"
	"augment2api/pkg/logger"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/go-redis/redis/v8"
	"github.com/google/uuid"
	"github.com/sirupsen/logrus"
)

// TokenInfo 存储token信息
type TokenInfo struct {
	Token           string    `json:"token"`
	TenantURL       string    `json:"tenant_url"`
	SessionID       string    `json:"session_id"`         // 绑定的会话ID
	UsageCount      int       `json:"usage_count"`        // 总对话次数
	ChatUsageCount  int       `json:"chat_usage_count"`   // CHAT模式对话次数
	AgentUsageCount int       `json:"agent_usage_count"`  // AGENT模式对话次数
	Remark          string    `json:"remark"`             // 备注字段
	InCool          bool      `json:"in_cool"`            // 是否在冷却中
	CoolEnd         time.Time `json:"cool_end,omitempty"` // 冷却结束时间
}

// TokenItem token项结构
type TokenItem struct {
	Token     string `json:"token"`
	TenantUrl string `json:"tenantUrl"`
}

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

// GetRedisTokenHandler 从Redis获取token列表，支持分页
func GetRedisTokenHandler(c *gin.Context) {
	// 获取分页参数（可选）
	page := c.DefaultQuery("page", "1")
	pageSize := c.DefaultQuery("page_size", "0") // 0表示不分页，返回所有

	pageNum, _ := strconv.Atoi(page)
	pageSizeNum, _ := strconv.Atoi(pageSize)

	if pageNum < 1 {
		pageNum = 1
	}

	// 获取所有token的key (使用通配符模式)
	keys, err := config.RedisKeys("token:*")
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"status": "error",
			"error":  "获取token列表失败: " + err.Error(),
		})
		return
	}

	// 如果没有token
	if len(keys) == 0 {
		c.JSON(http.StatusOK, gin.H{
			"status":      "success",
			"tokens":      []TokenInfo{},
			"total":       0,
			"page":        pageNum,
			"page_size":   pageSizeNum,
			"total_pages": 0,
		})
		return
	}

	// 对keys进行排序，确保顺序稳定
	sort.Sort(sort.Reverse(sort.StringSlice(keys)))

	// 使用并发方式批量获取token信息
	var wg sync.WaitGroup
	tokenList := make([]TokenInfo, 0, len(keys))
	tokenListChan := make(chan TokenInfo, len(keys))
	concurrencyLimit := 10 // 限制并发数
	sem := make(chan struct{}, concurrencyLimit)

	for _, key := range keys {
		// 从key中提取token (格式: "token:{token}")
		token := key[6:] // 去掉前缀 "token:"

		wg.Add(1)
		sem <- struct{}{} // 获取信号量

		go func(tokenKey string, tokenValue string) {
			defer wg.Done()
			defer func() { <-sem }() // 释放信号量

			// 使用HGETALL一次性获取所有字段，减少网络往返
			fields, err := config.RedisHGetAll(tokenKey)
			if err != nil {
				return // 跳过无效的token
			}

			// 检查必要字段
			tenantURL, ok := fields["tenant_url"]
			if !ok {
				return
			}

			// 检查token状态
			status, ok := fields["status"]
			if ok && status == "disabled" {
				return // 跳过被标记为不可用的token
			}

			// 获取备注信息
			remark := fields["remark"]

			// 获取session_id信息
			sessionID := fields["session_id"]

			// 获取token的冷却状态 (异步获取)
			coolStatus, _ := GetTokenCoolStatus(tokenValue)

			// 获取使用次数 (可以考虑将这些计数缓存在Redis中)
			chatCount := getTokenChatUsageCount(tokenValue)
			agentCount := getTokenAgentUsageCount(tokenValue)
			totalCount := chatCount + agentCount

			// 构建token信息并发送到channel
			tokenListChan <- TokenInfo{
				Token:           tokenValue,
				TenantURL:       tenantURL,
				SessionID:       sessionID,
				UsageCount:      totalCount,
				ChatUsageCount:  chatCount,
				AgentUsageCount: agentCount,
				Remark:          remark,
				InCool:          coolStatus.InCool,
				CoolEnd:         coolStatus.CoolEnd,
			}
		}(key, token)
	}

	// 启动一个goroutine来收集结果
	go func() {
		wg.Wait()
		close(tokenListChan)
	}()

	// 从channel中收集结果
	for info := range tokenListChan {
		tokenList = append(tokenList, info)
	}

	// 对token列表按照token字符串进行排序，确保每次刷新结果顺序一致
	sort.Slice(tokenList, func(i, j int) bool {
		return tokenList[i].Token > tokenList[j].Token // 降序排序
	})

	// 计算总页数和分页数据
	totalItems := len(tokenList)
	totalPages := 1

	// 如果需要分页
	if pageSizeNum > 0 {
		totalPages = (totalItems + pageSizeNum - 1) / pageSizeNum

		// 确保页码有效
		if pageNum > totalPages && totalPages > 0 {
			pageNum = totalPages
		}

		// 计算分页的起始和结束索引
		startIndex := (pageNum - 1) * pageSizeNum
		endIndex := startIndex + pageSizeNum

		if startIndex < totalItems {
			if endIndex > totalItems {
				endIndex = totalItems
			}
			tokenList = tokenList[startIndex:endIndex]
		} else {
			tokenList = []TokenInfo{}
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"status":      "success",
		"tokens":      tokenList,
		"total":       totalItems,
		"page":        pageNum,
		"page_size":   pageSizeNum,
		"total_pages": totalPages,
	})
}

// SaveTokenToRedis 保存token到Redis
func SaveTokenToRedis(token, tenantURL string) error {
	// 创建一个唯一的key，包含token和tenant_url
	tokenKey := "token:" + token

	// token已存在，则跳过
	exists, err := config.RedisExists(tokenKey)
	if err != nil {
		return err
	}
	if exists {
		return nil
	}

	// 将tenant_url存储在token对应的哈希表中
	err = config.RedisHSet(tokenKey, "tenant_url", tenantURL)
	if err != nil {
		return err
	}

	// 生成并存储session_id
	sessionID := uuid.New().String()
	err = config.RedisHSet(tokenKey, "session_id", sessionID)
	if err != nil {
		return err
	}

	// 默认将新添加的token标记为活跃状态
	err = config.RedisHSet(tokenKey, "status", "active")
	if err != nil {
		return err
	}

	// 初始化备注为空字符串
	return config.RedisHSet(tokenKey, "remark", "")
}

// DeleteTokenHandler 删除指定的token
func DeleteTokenHandler(c *gin.Context) {
	token := c.Param("token")
	if token == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"status": "error",
			"error":  "未指定token",
		})
		return
	}

	tokenKey := "token:" + token

	// 检查token是否存在
	exists, err := config.RedisExists(tokenKey)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"status": "error",
			"error":  "检查token失败: " + err.Error(),
		})
		return
	}

	if !exists {
		c.JSON(http.StatusNotFound, gin.H{
			"status": "error",
			"error":  "token不存在",
		})
		return
	}

	// 删除token
	if err := config.RedisDel(tokenKey); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"status": "error",
			"error":  "删除token失败: " + err.Error(),
		})
		return
	}

	// 删除token关联的使用次数（如果存在）
	// 删除总使用次数
	tokenUsageKey := "token_usage:" + token
	exists, err = config.RedisExists(tokenUsageKey)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"status": "error",
			"error":  "检查token使用次数失败: " + err.Error(),
		})
		return
	}
	if exists {
		if err := config.RedisDel(tokenUsageKey); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{
				"status": "error",
				"error":  "删除token使用次数失败: " + err.Error(),
			})
		}
	}

	// 删除CHAT模式使用次数
	tokenChatUsageKey := "token_usage_chat:" + token
	exists, err = config.RedisExists(tokenChatUsageKey)
	if err == nil && exists {
		config.RedisDel(tokenChatUsageKey)
	}

	// 删除AGENT模式使用次数
	tokenAgentUsageKey := "token_usage_agent:" + token
	exists, err = config.RedisExists(tokenAgentUsageKey)
	if err == nil && exists {
		config.RedisDel(tokenAgentUsageKey)
	}

	c.JSON(http.StatusOK, gin.H{
		"status": "success",
	})
}

// AddTokenHandler 批量添加token到Redis
func AddTokenHandler(c *gin.Context) {
	var tokens []TokenItem
	if err := c.ShouldBindJSON(&tokens); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"status": "error",
			"error":  "无效的请求数据",
		})
		return
	}

	// 检查是否有token数据
	if len(tokens) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{
			"status": "error",
			"error":  "token列表为空",
		})
		return
	}

	// 批量保存token
	successCount := 0
	failedTokens := make([]string, 0)

	for _, item := range tokens {
		// 验证token格式
		if item.Token == "" || item.TenantUrl == "" {
			failedTokens = append(failedTokens, item.Token)
			continue
		}

		// 保存到Redis
		err := SaveTokenToRedis(item.Token, item.TenantUrl)
		if err != nil {
			failedTokens = append(failedTokens, item.Token)
			continue
		}
		successCount++
	}

	// 返回处理结果
	result := gin.H{
		"status":        "success",
		"total":         len(tokens),
		"success_count": successCount,
	}

	if len(failedTokens) > 0 {
		result["failed_tokens"] = failedTokens
		result["failed_count"] = len(failedTokens)
	}

	c.JSON(http.StatusOK, result)
}

// CheckTokenTenantURL 检测token的租户地址
func CheckTokenTenantURL(token string, sessionID string) (string, error) {
	// 构建测试消息
	testMsg := map[string]interface{}{
		"message":              "hello，what is your name",
		"mode":                 "CHAT",
		"prefix":               "You are AI assistant,help me to solve problems!",
		"suffix":               " ",
		"lang":                 "HTML",
		"user_guidelines":      "You are a helpful assistant, you can help me to solve problems and always answer in Chinese.",
		"workspace_guidelines": "",
		"feature_detection_flags": map[string]interface{}{
			"support_raw_output": true,
		},
		"tool_definitions": []map[string]interface{}{},
		"blobs": map[string]interface{}{
			"checkpoint_id": nil,
			"added_blobs":   []string{},
			"deleted_blobs": []string{},
		},
	}

	jsonData, err := json.Marshal(testMsg)
	if err != nil {
		return "", fmt.Errorf("序列化测试消息失败: %v", err)
	}

	tokenKey := "token:" + token

	currentTenantURL, err := config.RedisHGet(tokenKey, "tenant_url")

	var tenantURLResult string
	var foundValid bool
	var tenantURLsToTest []string

	// 如果Redis中有有效的租户地址，优先测试该地址
	if err == nil && currentTenantURL != "" {
		tenantURLsToTest = append(tenantURLsToTest, currentTenantURL)
	}

	// 创建一个map来跟踪已添加的URL，避免重复
	uniqueTenantURLs := make(map[string]bool)
	if currentTenantURL != "" {
		uniqueTenantURLs[currentTenantURL] = true
	}

	// 添加其他租户地址
	// 添加 d1-d20 地址
	for i := 20; i >= 0; i-- {
		newTenantURL := fmt.Sprintf("https://d%d.api.augmentcode.com/", i)
		// 避免重复测试已有的租户地址
		if !uniqueTenantURLs[newTenantURL] {
			tenantURLsToTest = append(tenantURLsToTest, newTenantURL)
			uniqueTenantURLs[newTenantURL] = true
		}
	}

	// 添加 i0-i5 地址
	for i := 5; i >= 0; i-- {
		newTenantURL := fmt.Sprintf("https://i%d.api.augmentcode.com/", i)
		if !uniqueTenantURLs[newTenantURL] {
			tenantURLsToTest = append(tenantURLsToTest, newTenantURL)
			uniqueTenantURLs[newTenantURL] = true
		}
	}

	// 测试租户地址
	for _, tenantURL := range tenantURLsToTest {
		// 创建请求
		req, err := http.NewRequest("POST", tenantURL+"chat-stream", bytes.NewReader(jsonData))
		if err != nil {
			continue
		}

		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("User-Agent", config.AppConfig.UserAgent)
		req.Header.Set("x-api-version", "6")
		req.Header.Set("x-request-id", uuid.New().String())
		req.Header.Set("x-request-session-id", sessionID)

		client := createHTTPClient()
		resp, err := client.Do(req)
		if err != nil {
			fmt.Printf("请求失败: %v\n", err)
			continue
		}

		isInvalid := false
		func() {
			defer resp.Body.Close()

			// 检查是否返回401状态码（未授权）
			if resp.StatusCode == http.StatusUnauthorized {
				// 读取响应体内容
				buf := make([]byte, 1024)
				n, readErr := resp.Body.Read(buf)
				responseBody := ""
				if readErr == nil && n > 0 {
					responseBody = string(buf[:n])
				}

				// 只有当响应中包含"Invalid token"时才标记为不可用
				if readErr == nil && n > 0 && bytes.Contains(buf[:n], []byte("Invalid token")) {
					// 将token标记为不可用
					err = config.RedisHSet(tokenKey, "status", "disabled")
					if err != nil {
						fmt.Printf("标记token为不可用失败: %v\n", err)
					}
					logger.Log.WithFields(logrus.Fields{
						"token":         token,
						"response_body": responseBody,
					}).Info("token: 已被标记为不可用,返回401未授权")
					isInvalid = true
				}
				return
			}

			// 检查响应状态
			if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusPaymentRequired {
				// 尝试读取一小部分响应以确认是否有效
				buf := make([]byte, 1024)
				n, err := resp.Body.Read(buf)
				if err == nil && n > 0 {
					responseContent := string(buf[:n])

					// 检查是否启用了REMOVE_FREE环境变量
					if config.AppConfig.RemoveFree == "true" {
						// 检查响应内容是否包含订阅非活动信息
						const (
							subscriptionInactiveMsg = "Your subscription for account"
							inactiveMsg             = "is inactive"
							suspendedMsg            = "has been suspended. To continue, [purchase a subscription](https://app.augmentcode.com/account)"
							outOfMessagesMsg        = "You are out of user messages for account"
						)

						if (strings.Contains(responseContent, subscriptionInactiveMsg) &&
							(strings.Contains(responseContent, inactiveMsg) || strings.Contains(responseContent, suspendedMsg))) ||
							strings.Contains(responseContent, outOfMessagesMsg) {
							// 将token标记为不可用
							err = config.RedisHSet(tokenKey, "status", "disabled")
							if err != nil {
								fmt.Printf("标记token为不可用失败: %v\n", err)
							}
							logger.Log.WithFields(logrus.Fields{
								"token":         token,
								"response_body": responseContent,
							}).Info("token: 检测到订阅异状态，TOKEN已标记为不可用")
							isInvalid = true
							return
						}
					}

					// 更新Redis中的租户地址和状态
					err = config.RedisHSet(tokenKey, "tenant_url", tenantURL)
					if err != nil {
						return
					}
					// 将token标记为可用
					err = config.RedisHSet(tokenKey, "status", "active")
					if err != nil {
						fmt.Printf("标记token为可用失败: %v\n", err)
					}
					logger.Log.WithFields(logrus.Fields{
						"token":          token,
						"new_tenant_url": tenantURL,
					}).Info("token: 更新租户地址成功")
					tenantURLResult = tenantURL
					foundValid = true
				}
			}
		}()

		// 如果token无效，立即返回错误，不再测试其他地址
		if isInvalid {
			return "", fmt.Errorf("token被标记为不可用")
		}

		// 如果找到有效的租户地址，跳出循环
		if foundValid {
			return tenantURLResult, nil
		}
	}

	return "", fmt.Errorf("未找到有效的租户地址")
}

// CheckAllTokensHandler 批量检测所有token的租户地址
func CheckAllTokensHandler(c *gin.Context) {
	// 获取所有token的key
	keys, err := config.RedisKeys("token:*")
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"status": "error",
			"error":  "获取token列表失败: " + err.Error(),
		})
		return
	}

	if len(keys) == 0 {
		c.JSON(http.StatusOK, gin.H{
			"status":   "success",
			"total":    0,
			"updated":  0,
			"disabled": 0,
		})
		return
	}

	var wg sync.WaitGroup
	// 使用互斥锁保护计数器
	var mu sync.Mutex
	var updatedCount int
	var disabledCount int
	var validTokenCount int

	for _, key := range keys {
		// 获取token状态，跳过已标记为不可用的token
		status, err := config.RedisHGet(key, "status")
		if err == nil && status == "disabled" {
			continue // 跳过此token
		}

		// 计算有效token数量
		mu.Lock()
		validTokenCount++
		mu.Unlock()

		wg.Add(1)
		go func(key string) {
			defer wg.Done()

			// 从key中提取token
			token := key[6:] // 去掉前缀 "token:"

			// 获取当前的租户地址
			oldTenantURL, _ := config.RedisHGet(key, "tenant_url")

			// 获取token的session_id，如果没有则生成一个临时的
			sessionID, err := config.RedisHGet(key, "session_id")
			if err != nil {
				sessionID = uuid.New().String()
			}

			// 检测租户地址
			newTenantURL, err := CheckTokenTenantURL(token, sessionID)
			logger.Log.WithFields(logrus.Fields{
				"token":          token,
				"old_tenant_url": oldTenantURL,
				"new_tenant_url": newTenantURL,
			}).Info("检测token租户地址")

			mu.Lock()
			if err != nil && err.Error() == "token被标记为不可用" {
				disabledCount++
			} else if err == nil && newTenantURL != oldTenantURL {
				updatedCount++
			}
			mu.Unlock()
		}(key)
	}

	wg.Wait()

	c.JSON(http.StatusOK, gin.H{
		"status":   "success",
		"total":    validTokenCount,
		"updated":  updatedCount,
		"disabled": disabledCount,
	})
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
		// 如果key不存在，返回默认状态
		if errors.Is(err, redis.Nil) {
			return TokenRequestStatus{
				InProgress:    false,
				LastRequestAt: time.Time{}, // 零值时间
			}, nil
		}
		return TokenRequestStatus{}, err
	}

	// 解析JSON
	var status TokenRequestStatus
	if err := json.Unmarshal([]byte(statusJSON), &status); err != nil {
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
		// 如果key不存在，返回默认状态
		if errors.Is(err, redis.Nil) {
			return TokenCoolStatus{
				InCool:  false,
				CoolEnd: time.Time{}, // 零值时间
			}, nil
		}
		return TokenCoolStatus{}, err
	}

	// 解析JSON
	var coolStatus TokenCoolStatus
	if err := json.Unmarshal([]byte(coolStatusJSON), &coolStatus); err != nil {
		return TokenCoolStatus{}, err
	}

	// 检查冷却时间是否已过
	if time.Now().After(coolStatus.CoolEnd) {
		coolStatus.InCool = false
	}

	return coolStatus, nil
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

// getTokenChatUsageCount 获取token的CHAT模式使用次数
func getTokenChatUsageCount(token string) int {
	// 使用Redis中的计数器获取使用次数
	countKey := "token_usage_chat:" + token
	count, err := config.RedisGet(countKey)
	if err != nil {
		return 0 // 如果出错或不存在，返回0
	}
	countInt, err := strconv.Atoi(count)
	if err != nil {
		return 0
	}

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
	countInt, err := strconv.Atoi(count)
	if err != nil {
		return 0
	}

	return countInt
}

// UpdateTokenRemark 更新token的备注信息
func UpdateTokenRemark(c *gin.Context) {
	token := c.Param("token")
	if token == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"status": "error",
			"error":  "未指定token",
		})
		return
	}

	var req struct {
		Remark string `json:"remark"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"status": "error",
			"error":  "无效的请求数据",
		})
		return
	}

	tokenKey := "token:" + token

	// 检查token是否存在
	exists, err := config.RedisExists(tokenKey)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"status": "error",
			"error":  "检查token失败: " + err.Error(),
		})
		return
	}

	if !exists {
		c.JSON(http.StatusNotFound, gin.H{
			"status": "error",
			"error":  "token不存在",
		})
		return
	}

	// 更新备注
	err = config.RedisHSet(tokenKey, "remark", req.Remark)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"status": "error",
			"error":  "更新备注失败: " + err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"status": "success",
	})
}

// MigrateTokensSessionID 确保所有token都有session_id字段
func MigrateTokensSessionID() error {
	// 获取所有token的key
	keys, err := config.RedisKeys("token:*")
	if err != nil {
		return fmt.Errorf("获取token列表失败: %v", err)
	}

	for _, key := range keys {
		// 检查token状态，跳过不可用的token
		status, err := config.RedisHGet(key, "status")
		if err == nil && status == "disabled" {
			continue // 跳过被标记为不可用的token
		}

		// 检查是否已有session_id字段
		exists, err := config.RedisHExists(key, "session_id")
		if err != nil {
			logger.Log.Error("check session_id field of token %s failed: %v", key, err)
			continue
		}

		// 如果没有session_id字段，生成一个新的session_id
		if !exists {
			sessionID := uuid.New().String()
			err = config.RedisHSet(key, "session_id", sessionID)
			if err != nil {
				logger.Log.Error("add session_id field to token %s failed: %v", key, err)
				continue
			}
			logger.Log.Info("add session_id field to token %s success", key)
		}
	}
	logger.Log.Info("migrate session_id field to all tokens success!")

	return nil
}
