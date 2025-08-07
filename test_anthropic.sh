#!/bin/bash

# Anthropic API测试脚本

echo "=== 测试Anthropic非流式API ==="

# 非流式请求测试
curl -X POST http://localhost:27080/v1/messages \
  -H "Accept: application/json" \
  -H "x-api-key: your-api-key-here" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "claude-sonnet-4-20250514",
    "max_tokens": 1024,
    "messages": [
      {
        "role": "user",
        "content": "你好，请用中文回答：什么是人工智能？"
      }
    ]
  }'

echo -e "\n\n=== 测试Anthropic流式API ==="

# 流式请求测试
curl -X POST http://localhost:27080/v1/messages \
  -H "Accept: text/event-stream" \
  -H "x-api-key: your-api-key-here" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "claude-sonnet-4-20250514",
    "max_tokens": 1024,
    "stream": true,
    "messages": [
      {
        "role": "user",
        "content": "请用中文简单介绍一下Go语言的特点"
      }
    ]
  }'

echo -e "\n\n=== 测试完成 ==="
