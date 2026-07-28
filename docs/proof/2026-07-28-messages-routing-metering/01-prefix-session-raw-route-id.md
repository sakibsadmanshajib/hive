# Pre-fix: a raw LiteLLM route id is dispatched upstream unrouted and unmetered

Target      : http://localhost:8098  (pre-fix binary)
Principal   : Supabase session JWT. user_id=0d9e118e-d455-4390-bc67-2020af4f46b5 tenant_id=f52897a7-d866-4718-b87a-ee495b001707
Expectation : the pre-fix handler POSTs the caller's model straight to LiteLLM, where 'route-groq-fast' is a model_list entry, so a route id answers with a real completion having passed no alias resolution, no entitlement check and no credit reservation
Captured    : 2026-07-28T08:03:26Z

## Request

```http
POST /v1/messages HTTP/1.1
Host: localhost:8098
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

## Response  (HTTP 200, 4059 ms)

```
Content-Type: application/json

{"id":"msg_chatcmpl-6e30b4a5-0b93-4a55-97d5-52824a7f67ec","type":"message","role":"assistant","model":"route-groq-fast","content":[{"type":"text","text":"routing proof ok"}],"stop_reason":"end_turn","usage":{"input_tokens":78,"output_tokens":31}}
```
