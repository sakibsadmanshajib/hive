# Pre-fix: the API-key half of this surface was dead

Target      : http://localhost:8098  (pre-fix binary)
Principal   : Hive API key (hk_ prefix), account_id=e518472a-30df-4c27-bffb-9625be0c8513, allow_all_models
Expectation : 401: the pre-fix handler required a session principal and 401'd every API-key caller, which is why no reservation for /v1/messages could ever exist before this change
Captured    : 2026-07-28T08:10:17Z

## Request

```http
POST /v1/messages HTTP/1.1
Host: localhost:8098
Authorization: Bearer <hk_ API key, redacted>
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

## Response  (HTTP 401, 2653 ms)

```
Content-Type: application/json

{"error":{"code":"UNAUTHENTICATED","message":"missing user","request_id":"c81be0ba-7be3-42bc-80b1-a55b9aa32c77","type":"UNAUTHORIZED"}}
```
