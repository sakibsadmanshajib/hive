# Fixed: a raw LiteLLM route id is refused

Target      : http://localhost:8099  (fixed binary)
Principal   : Supabase session JWT. user_id=11111111-1111-4111-8111-111111111111 tenant_id=22222222-2222-4222-8222-222222222222
Expectation : 404: the request now resolves through routing.SelectRoute, and a route id matches no alias policy, so it is refused before any dispatch, with no provider name or route id in the message
Captured    : 2026-07-28T08:03:27Z

## Request

```http
POST /v1/messages HTTP/1.1
Host: localhost:8099
Authorization: Bearer <session JWT, redacted>
Content-Type: application/json

{
  "model": "route-groq-fast",
  "max_tokens": 32,
  "messages": [
    {
      "role": "user",
      "content": "Reply with exactly: routing proof ok"
    }
  ]
}
```

## Response  (HTTP 404, 1040 ms)

```http
Content-Type: application/json

{"error":{"code":"INVALID_REQUEST","message":"model not found","request_id":"e6f3ae11-9512-43d2-8156-3cd93cf815b0","type":"INTERNAL"}}
```
