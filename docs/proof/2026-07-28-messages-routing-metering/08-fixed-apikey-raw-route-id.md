# Fixed: an API-key caller cannot address a raw route id either

Target      : http://localhost:8099  (fixed binary)
Principal   : Hive API key (hk_ prefix), account_id=e518472a-30df-4c27-bffb-9625be0c8513, allow_all_models
Expectation : 404 with no credit reservation created
Captured    : 2026-07-28T08:10:17Z

## Request

```http
POST /v1/messages HTTP/1.1
Host: localhost:8099
Authorization: Bearer <hk_ API key, redacted>
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

## Response  (HTTP 404, 264 ms)

```
Content-Type: application/json

{"error":{"message":"The model `route-groq-fast` does not exist or you do not have access to it.","type":"invalid_request_error","param":null,"code":"model_not_found"}}
```
