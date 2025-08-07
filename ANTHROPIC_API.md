# Anthropic API 支持

本项目现在同时支持 OpenAI 和 Anthropic 两种 API 格式，并且都支持流式和非流式输出。

## 支持的端点

### OpenAI 格式
- `POST /v1/chat/completions` - OpenAI 兼容的聊天完成端点
- `POST /v1` - 简化的 OpenAI 端点
- `POST /v1/chat` - 简化的 OpenAI 聊天端点

### Anthropic 格式
- `POST /v1/messages` - Anthropic 兼容的消息端点

## Anthropic API 使用方法

### 请求格式

#### 非流式请求
```bash
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
        "content": "Hello, world"
      }
    ]
  }'
```

#### 流式请求
```bash
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
        "content": "Hello, world"
      }
    ]
  }'
```

### 请求参数

| 参数 | 类型 | 必需 | 描述 |
|------|------|------|------|
| `model` | string | 是 | 模型名称，如 "claude-sonnet-4-20250514" |
| `max_tokens` | integer | 是 | 最大生成的token数量 |
| `messages` | array | 是 | 消息数组，包含role和content |
| `stream` | boolean | 否 | 是否使用流式输出，默认为false |
| `temperature` | number | 否 | 温度参数，控制随机性 |

### 响应格式

#### 非流式响应
```json
{
  "id": "msg_1234567890",
  "type": "message",
  "role": "assistant",
  "content": [
    {
      "type": "text",
      "text": "Hello! How can I help you today?"
    }
  ],
  "model": "claude-sonnet-4-20250514",
  "stop_reason": "end_turn",
  "stop_sequence": null,
  "usage": {
    "input_tokens": 10,
    "output_tokens": 25
  }
}
```

#### 流式响应
流式响应使用 Server-Sent Events (SSE) 格式：

```
event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hello"}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"!"}}

event: message_stop
data: {"type":"message_stop"}
```

## 模型支持

项目支持以下模型类型：

- **Chat模式**: 模型名称以 `-chat` 结尾，如 `claude-4-chat`
- **Agent模式**: 模型名称以 `-agent` 结尾，如 `claude-4-agent`
- **默认模式**: 其他模型名称默认使用Chat模式

## 认证

Anthropic API 使用 `x-api-key` 头进行认证，而不是 OpenAI 的 `Authorization: Bearer` 格式。

## 测试

项目包含以下测试文件：

1. `test_anthropic.go` - Go语言测试程序
2. `test_anthropic.sh` - Bash测试脚本

运行测试前，请确保：
1. 服务器正在运行 (默认端口 27080)
2. 已配置正确的认证信息
3. 将测试文件中的 `your-api-key-here` 替换为实际的API密钥

## 兼容性

- ✅ 支持流式和非流式输出
- ✅ 支持多轮对话
- ✅ 支持温度参数
- ✅ 支持token使用统计
- ✅ 支持错误处理和重试机制
- ✅ 支持并发控制

## 注意事项

1. Anthropic API 的消息格式与 OpenAI 略有不同，但本项目会自动处理转换
2. 流式响应使用不同的事件格式，符合 Anthropic 的 SSE 规范
3. 认证方式不同：Anthropic 使用 `x-api-key`，OpenAI 使用 `Authorization: Bearer`
4. 响应结构不同，但都包含完整的使用统计信息
