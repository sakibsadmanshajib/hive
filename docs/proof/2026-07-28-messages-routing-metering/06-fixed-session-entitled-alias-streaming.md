# Fixed: streaming works end to end

Target      : http://localhost:8099  (fixed binary)
Principal   : Supabase session JWT. user_id=11111111-1111-4111-8111-111111111111 tenant_id=22222222-2222-4222-8222-222222222222
Expectation : 200 text/event-stream carrying the Anthropic event sequence (message_start, content_block_delta, message_delta, message_stop)
Captured    : 2026-07-28T08:03:39Z

## Request

```http
POST /v1/messages HTTP/1.1
Host: localhost:8099
Authorization: Bearer <session JWT, redacted>
Content-Type: application/json

{
  "model": "hive-fast",
  "max_tokens": 32,
  "stream": true,
  "messages": [
    {
      "role": "user",
      "content": "Reply with exactly: routing proof ok"
    }
  ]
}
```

## Response  (HTTP 200, 3565 ms)

```http
Cache-Control: no-cache
Content-Type: text/event-stream

event: message_start
data: {"type":"message_start","message":{"id":"msg_chatcmpl-28e8379f-f8ab-4739-baa6-b88eceb9a62c","type":"message","role":"assistant","model":"hive-fast","content":[],"stop_reason":null,"stop_sequence":null,"usage":{}}}

event: ping
data: {"type":"ping"}

event: content_block_start
data: {"type":"content_block_start","content_block":{"type":"text"}}

event: content_block_delta
data: {"type":"content_block_delta","delta":{"type":"text_delta","text":"routing"}}

event: content_block_stop
data: {"type":"content_block_stop"}

event: message_delta
data: {"type":"message_delta","delta":{"type":"message_delta","stop_reason":"max_tokens"},"usage":{"output_tokens":32}}

event: message_stop
data: {"type":"message_stop"}
```
