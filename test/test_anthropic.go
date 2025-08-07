package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// AnthropicTestRequest 测试用的Anthropic请求结构
type AnthropicTestRequest struct {
	Model     string                   `json:"model"`
	MaxTokens int                      `json:"max_tokens"`
	Messages  []AnthropicTestMessage   `json:"messages"`
	Stream    bool                     `json:"stream,omitempty"`
}

type AnthropicTestMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

func main() {
	// 测试非流式请求
	fmt.Println("=== 测试Anthropic非流式API ===")
	testNonStreamAPI()

	fmt.Println("\n=== 测试Anthropic流式API ===")
	testStreamAPI()
}

// testNonStreamAPI 测试非流式API
func testNonStreamAPI() {
	url := "http://localhost:27080/v1/messages"
	
	payload := AnthropicTestRequest{
		Model:     "claude-sonnet-4-20250514",
		MaxTokens: 1024,
		Messages: []AnthropicTestMessage{
			{
				Role:    "user",
				Content: "你好，请用中文回答：什么是人工智能？",
			},
		},
		Stream: false,
	}

	jsonData, err := json.Marshal(payload)
	if err != nil {
		fmt.Printf("序列化请求失败: %v\n", err)
		return
	}

	client := &http.Client{
		Timeout: 30 * time.Second,
	}
	
	req, err := http.NewRequest("POST", url, bytes.NewReader(jsonData))
	if err != nil {
		fmt.Printf("创建请求失败: %v\n", err)
		return
	}

	req.Header.Set("Accept", "application/json")
	req.Header.Set("x-api-key", "your-api-key-here") // 需要替换为实际的API密钥
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		fmt.Printf("请求失败: %v\n", err)
		return
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		fmt.Printf("读取响应失败: %v\n", err)
		return
	}

	fmt.Printf("状态码: %d\n", resp.StatusCode)
	fmt.Printf("响应: %s\n", string(body))
}

// testStreamAPI 测试流式API
func testStreamAPI() {
	url := "http://localhost:27080/v1/messages"
	
	payload := AnthropicTestRequest{
		Model:     "claude-sonnet-4-20250514",
		MaxTokens: 1024,
		Messages: []AnthropicTestMessage{
			{
				Role:    "user",
				Content: "请用中文简单介绍一下Go语言的特点",
			},
		},
		Stream: true,
	}

	jsonData, err := json.Marshal(payload)
	if err != nil {
		fmt.Printf("序列化请求失败: %v\n", err)
		return
	}

	client := &http.Client{
		Timeout: 60 * time.Second,
	}
	
	req, err := http.NewRequest("POST", url, bytes.NewReader(jsonData))
	if err != nil {
		fmt.Printf("创建请求失败: %v\n", err)
		return
	}

	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("x-api-key", "your-api-key-here") // 需要替换为实际的API密钥
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		fmt.Printf("请求失败: %v\n", err)
		return
	}
	defer resp.Body.Close()

	fmt.Printf("状态码: %d\n", resp.StatusCode)
	fmt.Printf("Content-Type: %s\n", resp.Header.Get("Content-Type"))
	
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		fmt.Printf("错误响应: %s\n", string(body))
		return
	}

	fmt.Println("流式响应:")
	
	// 读取流式响应
	buffer := make([]byte, 4096)
	for {
		n, err := resp.Body.Read(buffer)
		if err != nil {
			if err == io.EOF {
				break
			}
			fmt.Printf("读取流式响应失败: %v\n", err)
			break
		}
		
		chunk := string(buffer[:n])
		lines := strings.Split(chunk, "\n")
		
		for _, line := range lines {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			
			if strings.HasPrefix(line, "event:") {
				fmt.Printf("事件: %s\n", strings.TrimPrefix(line, "event:"))
			} else if strings.HasPrefix(line, "data:") {
				data := strings.TrimPrefix(line, "data:")
				data = strings.TrimSpace(data)
				if data != "" {
					fmt.Printf("数据: %s\n", data)
				}
			}
		}
	}
}
