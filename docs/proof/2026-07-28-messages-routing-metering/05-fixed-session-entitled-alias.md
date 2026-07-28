# Fixed: an entitled alias succeeds, through SelectRoute

Target      : http://localhost:8099  (fixed binary)
Principal   : Supabase session JWT. user_id=11111111-1111-4111-8111-111111111111 tenant_id=22222222-2222-4222-8222-222222222222
Expectation : 200 with an Anthropic-shaped message whose model field echoes the client alias, never the resolved route
Captured    : 2026-07-28T08:03:35Z

## Request

```http
POST /v1/messages HTTP/1.1
Host: localhost:8099
Authorization: Bearer <session JWT, redacted>
Content-Type: application/json

{
  "model": "hive-fast",
  "max_tokens": 32,
  "messages": [
    {
      "role": "user",
      "content": "Reply with exactly: routing proof ok"
    }
  ]
}
```

## Response  (HTTP 200, 6319 ms)

```http
Content-Type: application/json

{"id":"msg_chatcmpl-645dc981-80d2-42d1-aff9-c162fe9ccc9f","type":"message","role":"assistant","model":"hive-fast","content":null,"stop_reason":"max_tokens","usage":{"input_tokens":78,"output_tokens":32}}
```
