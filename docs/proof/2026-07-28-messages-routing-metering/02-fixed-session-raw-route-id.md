# Fixed: a raw LiteLLM route id is refused

Target      : http://localhost:8099  (fixed binary)
Principal   : Supabase session JWT. user_id=0d9e118e-d455-4390-bc67-2020af4f46b5 tenant_id=f52897a7-d866-4718-b87a-ee495b001707
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

```
Content-Type: application/json

{"error":{"code":"INVALID_REQUEST","message":"model not found","request_id":"e6f3ae11-9512-43d2-8156-3cd93cf815b0","type":"INTERNAL"}}
```
