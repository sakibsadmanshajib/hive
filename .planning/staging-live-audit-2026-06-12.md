# Staging Live Audit — 2026-06-12

Audited by: E2E agent (automated)
Date: 2026-06-12
Staging base URLs: `https://api-hive.scubed.co` (edge), `https://cp-hive.scubed.co` (control-plane), `https://console-hive.scubed.co` (web-console, Cloudflare Workers)
API key used: `hk_aIqD_...qdM` (last 3 chars; full key masked)

---

## Surface Status Summary

| Surface | Status | Evidence |
|---|---|---|
| Edge API health | PASS | `GET /health` 200 `{"status":"ok"}` |
| Edge API auth guard | PASS | `GET /v1/models` no key 401 correct OpenAI-format error |
| Edge API models list | PASS | `GET /v1/models` with key 200, 4 models |
| Inference: hive-default | PASS | 200, content="AUDIT_OK", finish_reason=stop |
| Inference: hive-fast | PARTIAL | Works at max_tokens>=200; empty content at max_tokens=20 (reasoning model token drain) |
| Inference: hive-auto | FAIL | 404 "model does not exist or you do not have access to it" |
| Tools (function calling) | PASS | 200, tool_calls present with correct arguments |
| Embeddings | PASS | 200, float array returned |
| Control-plane health | PASS | `GET /health` 200 `{"status":"ok"}` |
| Control-plane catalog | PASS | `GET /api/v1/catalog/models` 200, 4 models with pricing |
| Control-plane signup precheck | PASS | `POST /api/v1/auth/sign-up/precheck` 200 `{"status":"ok"}` |
| Control-plane auth (API key) | NOT APPLICABLE | CP protected routes require Supabase JWT, not Hive API key — expected architecture |
| Chat UI (Open WebUI) | NOT DEPLOYED | chat-hive.scubed.co, owui-hive.scubed.co: no DNS/no server — intentionally excluded from staging compose |
| Web-console load | PASS | 200, Next.js renders sign-in page correctly |
| Web-console signup form | PASS | /auth/sign-up renders email+password fields |
| Web-console signup submit | PASS | Supabase auth called, "Check your email" confirmation screen shown |

---

## A. Edge API

### A1. Health and Auth Guard

```
GET https://api-hive.scubed.co/health         200  {"status":"ok"}
GET https://api-hive.scubed.co/v1/models      401  (no key — correct OpenAI-format error body)
GET https://api-hive.scubed.co/v1/models      200  (with key — 4 models returned)
Models: hive-auto, hive-default, hive-embedding-default, hive-fast
```

### A2. Inference

#### hive-default — PASS

```
POST /v1/chat/completions  model=hive-default  messages=[{user:"Say: AUDIT_OK"}]  max_tokens=15
Response 200:
  content="AUDIT_OK"
  finish_reason=stop
  prompt_tokens=19  completion_tokens=5  total_tokens=24
  reasoning_tokens=0
  system_fingerprint=vllm-0.21.1rc1.dev262+g33d7cbe02-tp8-dd71347d
```

#### hive-fast — PARTIAL (reasoning model token budget defect)

```
POST /v1/chat/completions  model=hive-fast  messages=[{user:"Reply with exactly: AUDIT_OK"}]  max_tokens=20
Response 200:
  content=""  (empty string)
  finish_reason=length
  completion_tokens=20  reasoning_tokens=18

POST /v1/chat/completions  model=hive-fast  messages=[{user:"Say hello in one word"}]  max_tokens=200
Response 200:
  content="Hello"
  finish_reason=stop
  completion_tokens=67  reasoning_tokens=57
  system_fingerprint=fp_8b41efc9a3
```

Finding: the model behind `hive-fast` on staging is a thinking/reasoning model. 57 of 67 tokens are consumed by hidden chain-of-thought. With max_tokens=20 the entire budget is exhausted by reasoning and the visible content field is empty string. The response is 200, so clients receive no error signal. The seed migration sets hive-fast to `groq/llama-3.3-70b-versatile` but the staging routing DB appears to point elsewhere (fingerprint mismatch). Any caller using max_tokens below ~60 will receive a silent empty response.

#### hive-auto — FAIL

```
POST /v1/chat/completions  model=hive-auto  messages=[{user:"Say: AUDIT_OK"}]  max_tokens=15
Response 404:
  {"error":{"message":"The model `hive-auto` does not exist or you do not have access to it.",
   "type":"invalid_request_error","code":"model_not_found"}}
```

`hive-auto` is listed in `/v1/models` and the CP catalog but inference via edge returns 404. The alias has no working LiteLLM route on staging.

#### Tools (function calling) — PASS

```
POST /v1/chat/completions  model=hive-fast  tools=[get_weather(location)]  tool_choice=auto
Response 200:
  finish_reason=tool_calls
  tool_calls=[{id:"fc_b891...", type:"function",
               function:{name:"get_weather", arguments:'{"location":"Dhaka"}'}}]
```

#### Embeddings — PASS

```
POST /v1/embeddings  model=hive-embedding-default  input="Hello world"
Response 200:
  object=list  data[0].object=embedding
  embedding=[0.0247..., 0.0142..., ...]  (1536-dimension float array)
```

---

## B. Chat UI (Open WebUI)

**NOT DEPLOYED to staging.**

Evidence:
- `fetch('https://chat-hive.scubed.co/')` — ERROR: fetch failed (no TCP connection)
- `fetch('https://owui-hive.scubed.co/')` — ERROR: fetch failed (no TCP connection)

Root cause confirmed by source: `docker-compose.yml` defines `open-webui` and `caddy-owui` services under the `chat` and `enterprise` compose profiles only. The staging compose (`docker-compose.staging.yml`) does not activate either profile. The staging VM is a 1 GB OCI instance; Open WebUI alone requires approximately 500 MB RAM which would leave insufficient headroom for the core stack (edge-api + control-plane + LiteLLM + Redis).

Conclusion: Chat UI is intentionally a local/enterprise-only feature. Staging is API + web-console only by design.

---

## C. Account Creation / Signup

### Signup Precheck Endpoint

```
POST https://cp-hive.scubed.co/api/v1/auth/sign-up/precheck
Body: {"email":"audit-test@example.com"}
Response 200: {"status":"ok"}
```

PASS. Endpoint live and responsive.

Note: precheck is registered conditionally in main.go (line 803) — it only mounts when the identity wiring is ready. The 200 confirms the Phase 19 identity wiring is active on staging.

### Full Signup via Web-Console

Playwright test on `https://console-hive.scubed.co/auth/sign-up`:

1. Page renders: h1="Create your Hive account", email input, password input present.
2. Filled email `staging-audit-test-1781236687150@mailnull.com`, password `[masked]`.
3. Clicked "Create account" button.
4. Network call: `POST HOST/auth/v1/signup?redirect_to=https://console-hive.scubed.co/auth/callback` (Supabase)
5. Result page: "ALMOST THERE / Check your email — We sent a verification link to staging-audit-test-...@mailnull.com."

PASS. Signup flow reaches email verification step. Full account activation requires email click-through (not testable without real mailbox access). The Supabase signup webhook (`/internal/auth/user-created`) fires post-confirmation — not directly testable here.

### Control-Plane Protected Routes

```
GET https://cp-hive.scubed.co/api/v1/accounts/current/credits/balance
  Authorization: Bearer hk_aIqD_...(API key)
Response 401: {"error":"invalid or expired token"}
```

Expected: CP protected routes require a Supabase JWT (browser session token), not a Hive API key. API keys are edge-layer tokens only. This is by design.

---

## D. Web-Console (Cloudflare Workers)

URL: `https://console-hive.scubed.co`

```
GET https://console-hive.scubed.co/   200  text/html  Next.js app
  Final URL (after redirect): /auth/sign-in
  Title: "Hive Console"
  H1: "Sign in to your console"
  Body text: "WELCOME BACK / Sign in to your console / Manage API keys, credits, and usage analytics for your workspace."
  Elements: email input, password input, "Continue" button, "Don't have an account? Create one" link
  Footer: "© 2026 Hive / api.hive · v1"

GET https://console-hive.scubed.co/auth/sign-up   200
  H1: "Create your Hive account"
  Body: "Free to start. Pay only for what you use, in BDT."
  Elements: email input, password input, "Create account" button
```

PASS. The Cloudflare Workers deployment is live and correctly serving the Next.js console. No JS errors detected by Playwright.

---

## Prioritised Gap List for MVP Demo

### P0 — Blocking

**1. hive-auto inference returns 404.**
The alias appears in `/v1/models` and CP catalog but fails on inference. Any client using `hive-auto` gets a 404. Fix: verify the `hive-auto` alias has a working `litellm_model_name` in the `routing_policies` DB table on staging and a matching route in `deploy/litellm/config.yaml`.

**2. hive-fast empty response at low max_tokens.**
With `max_tokens` under ~60, the reasoning model consumes all tokens on hidden chain-of-thought and returns empty content with status 200. Callers receive no error signal. Fix: either (a) revert `hive-fast` staging route to a non-reasoning model (`groq/llama-3.3-70b-versatile` as per seed migration), or (b) add an edge validation guard that returns 400 for requests to reasoning-backed aliases below a safe `max_tokens` threshold, with a clear error message.

### P1 — Important for Demo

**3. hive-fast reasoning overhead mismatches "fast" positioning.**
57 reasoning tokens per simple response adds latency and cost for a model marketed as the fast tier. Confirm whether the staging routing DB drift (away from the seeded llama-3.3-70b-versatile) is intentional. If a reasoning model is intended as hive-fast, update the alias description and set a minimum max_tokens policy.

**4. Control-plane account routes not externally testable without browser session.**
CP `/api/v1/accounts/current/*` requires Supabase JWT. For a demo walkthrough of billing/credits, a signed-in browser session is required. Ensure demo account credentials are available.

### P2 — Post-MVP

**5. Chat UI (OWUI) not on staging.** Intentionally excluded. Should be noted in demo materials — staging is API-only.

**6. Signup email verification not automatable.** Full activation requires mailbox access. Consider a seeded demo account for walkthroughs.

**7. `amount_usd` BD checkout exposure.** Known issue tracked in project CLAUDE.md. Not directly tested here (requires a BD-locale payment flow with a live payment session).

---

## Request Evidence Log (masked)

All requests made 2026-06-12. Keys masked. IPs not recorded.

| Request | Status | Finding |
|---|---|---|
| GET api-hive.scubed.co/health | 200 | {"status":"ok"} |
| GET api-hive.scubed.co/v1/models (no key) | 401 | correct OpenAI-format auth error |
| GET api-hive.scubed.co/v1/models (with key) | 200 | 4 models listed |
| POST /v1/chat/completions hive-fast max_tokens=20 | 200 | content="" empty (reasoning drain) |
| POST /v1/chat/completions hive-fast max_tokens=200 | 200 | content="Hello" works |
| POST /v1/chat/completions hive-default max_tokens=15 | 200 | content="AUDIT_OK" |
| POST /v1/chat/completions hive-auto max_tokens=15 | 404 | model not found |
| POST /v1/chat/completions hive-fast + tools | 200 | tool_calls present, correct args |
| POST /v1/embeddings hive-embedding-default | 200 | float array returned |
| GET cp-hive.scubed.co/health | 200 | {"status":"ok"} |
| GET cp-hive.scubed.co/api/v1/catalog/models | 200 | 4 models with pricing |
| POST cp-hive.scubed.co/api/v1/auth/sign-up/precheck | 200 | {"status":"ok"} |
| GET cp-hive.scubed.co/api/v1/accounts/current/credits/balance | 401 | JWT required (expected) |
| GET chat-hive.scubed.co | FAIL | fetch failed — not deployed |
| GET owui-hive.scubed.co | FAIL | fetch failed — not deployed |
| GET console-hive.scubed.co | 200 | Next.js renders, redirects to sign-in |
| Playwright: /auth/sign-up load | 200 | form renders correctly |
| Playwright: signup submit | 200 | "Check your email" shown |
