package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// TestRequest 测试请求结构
type TestRequest struct {
	Model    string        `json:"model"`
	Messages []TestMessage `json:"messages"`
	Stream   bool          `json:"stream"`
}

type TestMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

func main() {
	fmt.Println("=== 测试Token自动切换功能 ===")
	
	// 测试OpenAI格式
	fmt.Println("\n1. 测试OpenAI格式API")
	testOpenAIAPI()
	
	// 测试Anthropic格式
	fmt.Println("\n2. 测试Anthropic格式API")
	testAnthropicAPI()
}

func testOpenAIAPI() {
	url := "http://localhost:27080/v1/chat/completions"
	
	payload := TestRequest{
		Model: "claude-4-chat",
		Messages: []TestMessage{
			{
				Role:    "user",
				Content: "你好，请简单介绍一下你自己",
			},
		},
		Stream: false,
	}
	
	sendRequest(url, payload, "OpenAI")
}

func testAnthropicAPI() {
	url := "http://localhost:27080/v1/messages"
	
	// Anthropic格式的请求
	anthropicPayload := map[string]interface{}{
		"model":      "claude-4-chat",
		"max_tokens": 1024,
		"messages": []map[string]interface{}{
			{
				"role":    "user",
				"content": "你好，请简单介绍一下你自己",
			},
		},
		"stream": false,
	}
	
	sendAnthropicRequest(url, anthropicPayload, "Anthropic")
}

func sendRequest(url string, payload TestRequest, apiType string) {
	jsonData, err := json.Marshal(payload)
	if err != nil {
		fmt.Printf("%s API - 序列化请求失败: %v\n", apiType, err)
		return
	}
	
	client := &http.Client{
		Timeout: 30 * time.Second,
	}
	
	req, err := http.NewRequest("POST", url, bytes.NewReader(jsonData))
	if err != nil {
		fmt.Printf("%s API - 创建请求失败: %v\n", apiType, err)
		return
	}
	
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer test-token")
	
	fmt.Printf("%s API - 发送请求到: %s\n", apiType, url)
	
	resp, err := client.Do(req)
	if err != nil {
		fmt.Printf("%s API - 请求失败: %v\n", apiType, err)
		return
	}
	defer resp.Body.Close()
	
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		fmt.Printf("%s API - 读取响应失败: %v\n", apiType, err)
		return
	}
	
	fmt.Printf("%s API - 响应状态码: %d\n", apiType, resp.StatusCode)
	
	if resp.StatusCode == 429 {
		fmt.Printf("%s API - 检测到429错误，Token切换功能应该会自动重试\n", apiType)
	}
	
	// 打印响应内容的前200个字符
	responseStr := string(body)
	if len(responseStr) > 200 {
		responseStr = responseStr[:200] + "..."
	}
	fmt.Printf("%s API - 响应内容: %s\n", apiType, responseStr)
}

func sendAnthropicRequest(url string, payload map[string]interface{}, apiType string) {
	jsonData, err := json.Marshal(payload)
	if err != nil {
		fmt.Printf("%s API - 序列化请求失败: %v\n", apiType, err)
		return
	}
	
	client := &http.Client{
		Timeout: 30 * time.Second,
	}
	
	req, err := http.NewRequest("POST", url, bytes.NewReader(jsonData))
	if err != nil {
		fmt.Printf("%s API - 创建请求失败: %v\n", apiType, err)
		return
	}
	
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", "test-token")
	
	fmt.Printf("%s API - 发送请求到: %s\n", apiType, url)
	
	resp, err := client.Do(req)
	if err != nil {
		fmt.Printf("%s API - 请求失败: %v\n", apiType, err)
		return
	}
	defer resp.Body.Close()
	
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		fmt.Printf("%s API - 读取响应失败: %v\n", apiType, err)
		return
	}
	
	fmt.Printf("%s API - 响应状态码: %d\n", apiType, resp.StatusCode)
	
	if resp.StatusCode == 429 {
		fmt.Printf("%s API - 检测到429错误，Token切换功能应该会自动重试\n", apiType)
	}
	
	// 打印响应内容的前200个字符
	responseStr := string(body)
	if len(responseStr) > 200 {
		responseStr = responseStr[:200] + "..."
	}
	fmt.Printf("%s API - 响应内容: %s\n", apiType, responseStr)
}
