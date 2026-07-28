# Fixed: a raw route id is refused on a streaming request too

Target      : http://localhost:8099  (fixed binary)
Principal   : Supabase session JWT. user_id=11111111-1111-4111-8111-111111111111 tenant_id=22222222-2222-4222-8222-222222222222
Expectation : 404, and the refusal is not delivered as a stream
Captured    : 2026-07-28T08:03:28Z

## Request

```http
POST /v1/messages HTTP/1.1
Host: localhost:8099
Authorization: Bearer <session JWT, redacted>
Content-Type: application/json

{
  "model": "route-groq-fast",
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

## Response  (HTTP 404, 514 ms)

```http
Content-Type: application/json

{"error":{"code":"INVALID_REQUEST","message":"model not found","request_id":"04beb899-29a7-4c4a-8e80-c9e5f95e842b","type":"INTERNAL"}}
```
