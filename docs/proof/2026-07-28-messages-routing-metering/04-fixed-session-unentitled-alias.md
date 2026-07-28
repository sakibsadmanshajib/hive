# Fixed: an alias the tenant is not entitled to is refused

Target      : http://localhost:8099  (fixed binary)
Principal   : Supabase session JWT. user_id=11111111-1111-4111-8111-111111111111 tenant_id=22222222-2222-4222-8222-222222222222
Expectation : 403: 'hive-default' is hidden from this tenant in tenant_model_visibility, and SelectRoute enforces that; the message names only the model the caller already asked for
Captured    : 2026-07-28T08:03:29Z

## Request

```http
POST /v1/messages HTTP/1.1
Host: localhost:8099
Authorization: Bearer <session JWT, redacted>
Content-Type: application/json

{
  "model": "hive-default",
  "max_tokens": 32,
  "messages": [
    {
      "role": "user",
      "content": "Reply with exactly: routing proof ok"
    }
  ]
}
```

## Response  (HTTP 403, 1042 ms)

```http
Content-Type: application/json

{"error":{"code":"FORBIDDEN","message":"model not available for this workspace","request_id":"b8c6a24f-4025-41e4-9dfc-c64359de507a","type":"FORBIDDEN"}}
```
