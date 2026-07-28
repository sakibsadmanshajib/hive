# Fixed: an entitled alias succeeds and the call is metered

Target      : http://localhost:8099  (fixed binary)
Principal   : Hive API key (`hk_` prefix), account_id=33333333-3333-4333-8333-333333333333, allow_all_models
Expectation : 200, with a credit reservation held before dispatch and settled at the upstream token count afterwards
Captured    : 2026-07-28T08:10:42Z

## Request

```http
POST /v1/messages HTTP/1.1
Host: localhost:8099
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

## Response  (HTTP 200, 17358 ms)

```http
Content-Type: application/json

{"id":"msg_chatcmpl-03662298-b1e5-42d5-b3b2-e0c94043b9e8","type":"message","role":"assistant","model":"hive-fast","content":null,"stop_reason":"max_tokens","usage":{"input_tokens":78,"output_tokens":32}}
```
