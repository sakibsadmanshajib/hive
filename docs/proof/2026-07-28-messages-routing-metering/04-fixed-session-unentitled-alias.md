# Fixed: an alias the tenant is not entitled to is refused

Target      : http://localhost:8099  (fixed binary)
Principal   : Supabase session JWT. user_id=0d9e118e-d455-4390-bc67-2020af4f46b5 tenant_id=f52897a7-d866-4718-b87a-ee495b001707
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

```
Content-Type: application/json

{"error":{"code":"FORBIDDEN","message":"model not available for this workspace","request_id":"b8c6a24f-4025-41e4-9dfc-c64359de507a","type":"FORBIDDEN"}}
```
