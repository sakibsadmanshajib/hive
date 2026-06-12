# Hive: 10-Minute Investor Demo Script

> **Purpose:** A tight, 10-minute live walkthrough for investors. Every beat is grounded in what runs **today** unless explicitly marked `[pending: X]`.
> **Date:** 2026-06-11
> **Presenter target time:** 10:00 total. Section timings are budgets, not floors.
> **Public surfaces only:** This script references public URLs (`api-hive.scubed.co`, planned `hive.scubed.com.bd`) and no private identifiers, keys, or account data.

---

## Reality grounding (read before presenting)

What is **live today** vs **pending** (source: `README.md`, `.planning/MVP.md`, `.planning/STATE.md`, git log):

| Capability | State | Source of truth |
|---|---|---|
| OpenAI-compatible API (`/v1` chat, embeddings, files, images, audio) | **Live** (v1.0 shipped 2026-04-21) | MVP capability read |
| Prepaid BDT billing (bKash, SSLCommerz, Stripe), `math/big` FX | **Live** (v1.0) | README, MVP |
| Chat UI (Open WebUI behind Caddy) | **Live** (Phase 19 merged) | MVP capability read |
| File RAG (upload, doc Q&A) | **Live**, Open WebUI native | MVP |
| Bangla locale (bn-BD) in chat | **Staged for upstream** (PR #196); enablement plus staging deploy `[pending: Phase 25]` | git log, STATE |
| `cloud` plus `enterprise` compose profiles | **Live** (PR #194 merged) | docker-compose.yml |
| Optional Ollama backend (EnterpriseEdge) | **Live** (config-only, enterprise profile) | docker-compose.yml |
| Provider catalog schema, custom providers, tenant model visibility, tools capability flag | **Merged** (PRs #197, #199) | git log |
| Tool/`tool_choice` passthrough at edge-api | `[pending: Phase 20-05]`, capability flag merged but edge-api passthrough not yet wired | Phase 20 PLAN.md |
| Hardware advisor and one-line installer wizard | `[pending: Phase 30, v1.3]` | v1.3 device doc |
| DGX Spark / RTX Spark hardware in hand | `[pending: post-funding]`, demo runs on a dev machine | MVP owner answers |

**Honesty rule for the room:** when a beat is pending, say so in one sentence and show the closest live proxy. Investors reward candor; a caught overclaim kills the round.

---

## Pre-flight checklist (do 30 minutes before)

- [ ] Laptop on a known-good network; phone hotspot ready as backup.
- [ ] `api-hive.scubed.co` health check returns 200: `curl -s https://api-hive.scubed.co/health`.
- [ ] A funded demo API key exported in the terminal env (never shown on screen): `export HIVE_KEY=<DEMO_KEY>`.
- [ ] Terminal font 18pt or larger, dark theme, prompt cleaned of any identifiers.
- [ ] Chat workstation tab pre-loaded `[pending: Phase 25 staging URL]`. **Fallback:** local `docker compose --profile enterprise --profile chat up` on `http://localhost:8090`.
- [ ] EnterpriseEdge: clean VM or local stack pre-pulled so `up` is fast.
- [ ] Recorded GIF/MP4 of every live beat saved locally (see per-beat fallback notes). If network drops, switch to the recording without breaking stride.
- [ ] Slides: market-numbers slide, roadmap board, ask slide loaded and on the right monitor.

---

## 1. Cold open: the problem (60s)

**On screen:** Single market-numbers slide.

**Slide content (from `.planning/MVP.md` market read):**
- **170M people.** No Bangladesh-localized, ChatGPT-class product bills in BDT.
- Global AI products price in **USD and require foreign cards**, a hard barrier for BD consumers and SMEs.
- BD developers have **no OpenAI-compatible API billable in BDT**.
- A **near-term enterprise self-host window**: DGX Spark ships now ($4,699 retail), RTX Spark class lands fall 2026. No local player serves it.

**Presenter script (verbatim, ~45s):**
> "A hundred and seventy million people. Not one AI platform takes bKash. A developer in Dhaka who wants to ship an AI feature is blocked at the checkout: every global API wants a US dollar card most of them cannot get. We built the gateway that takes local money and speaks the same API the whole world already codes against. Let me show you three things that work today, and one that's coming."

**Presenter note:** Do not read the slide. Say the numbers once, then move. The slide stays up for the full 60s.

**Fallback:** Slide is local; no network needed. No GIF required for the cold open.

---

## 2. Chat workstation (2 min)

**Goal:** Show a consumer-grade chat product with the Bangla angle and file RAG, all on Hive's own stack.

**Beat A, open the chat (20s).**
- Live at the staging chat URL `[pending: Phase 25 deploy]`.
- **Fallback (works today):** local Open WebUI via `docker compose --env-file ../../.env --profile enterprise --profile chat up --build`, reachable at `http://localhost:8090` (Caddy in front of Open WebUI).
- Send one English prompt. Show streaming response.

**Beat B, Bangla angle (40s).**
- Type a prompt in Bangla; the model answers in Bangla.
- Narrate: "The chat layer is Open WebUI on our stack. The Bangla locale is staged as an upstream contribution `[pending: Phase 25]`. Today I'll demo the model answering in Bangla, and the localized UI chrome lands with the staging deploy."
- **Presenter note:** Be precise. The *model* speaks Bangla today; the *UI locale* (bn-BD chrome) is the pending piece. Don't blur the two.

**Beat C, file RAG (40s).**
- Drag a PDF into the chat (use a neutral public document, never a borrower, lender, or any confidential file).
- Ask a question only answerable from the document. Show the grounded answer.
- Narrate: "This is Open WebUI's native RAG, running entirely on our box. For EnterpriseEdge, that file never leaves the customer's server."

**Fallback plan:** Pre-record a GIF of the full flow (English prompt, Bangla answer, PDF drop, grounded answer). If the staging URL is down or network fails, run the same flow on localhost; if the local stack is cold, play the GIF and narrate live.

---

## 3. Developer API (2 min)

**Goal:** Prove the core wedge. Switch from OpenAI to Hive with a base-URL and key change, billed in BDT.

**Beat A, raw curl, free model (40s).** Real command against the live staging host:

```bash
curl https://api-hive.scubed.co/v1/chat/completions \
  -H "Authorization: Bearer $HIVE_KEY" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "llama-3.3-70b",
    "messages": [{"role": "user", "content": "Say hello to our investors in one sentence."}]
  }'
```

- Narrate: "Standard OpenAI chat-completions shape. Free model, routed provider-agnostically behind the gateway. Billed in BDT credits."
- **Presenter note:** The `model` value is a Hive alias resolved by the catalog; the free backing route is an OpenRouter `:free` model. Keep the key in an env var, never on screen.

**Beat B, same code, OpenAI SDK pointed at Hive (40s).** The "one-line switch" moment:

```python
from openai import OpenAI

client = OpenAI(
    base_url="https://api-hive.scubed.co/v1",
    api_key="<HIVE_KEY>",
)

resp = client.chat.completions.create(
    model="llama-3.3-70b",
    messages=[{"role": "user", "content": "Same call, official OpenAI SDK."}],
)
print(resp.choices[0].message.content)
```

- Narrate: "This is the official OpenAI SDK. The only change from a real OpenAI app is the base URL and the key. Every existing OpenAI codebase in Bangladesh is a drop-in customer."

**Beat C, tools array (40s).**
- Show a request carrying a `tools` array against a tool-capable alias.
- **Honest framing:** "Provider capability routing, the catalog that knows which models support tools, just merged (PR #197). The edge-API passthrough that forwards the `tools` array to a capable route is the last wire `[pending: Phase 20-05]`. Here's the capability flag live in the catalog today; the passthrough lands this phase."
- **Presenter note:** Do NOT claim tool calls fully execute end-to-end today. Show the capability schema and flag (merged) and state the passthrough is the pending step. If it lands before the demo, swap this note and show a real tool round-trip.

**Fallback plan:** Pre-record a GIF of the curl plus SDK calls returning real completions. If the network or staging host is down, play the GIF. For Beat C, a screen capture of the merged capability flag in the catalog (or the Phase 20 plan) carries the point honestly.

---

## 4. EnterpriseEdge (2 min)

**Goal:** The sovereign-AI pitch. Banks and government cannot use foreign cloud; this is their box.

**Beat A, one-liner on a clean VM (50s).**
- On a clean VM (or local), bring up the self-hosted stack:

```bash
cd deploy/docker
docker compose --env-file ../../.env --profile enterprise up --build
```

- Narrate: "One compose profile. Gateway, chat UI, model router, and an optional local Ollama backend, all on one box, no external dependency. Same codebase as our cloud; one flag flips it to self-hosted."
- **Presenter note:** The `enterprise` profile is live (PR #194). A polished single-command installer wizard (curl-pipe bootstrap) is `[pending: Phase 30, v1.3]`.

**Beat B, hardware advisor moment (40s).**
- **This beat is `[pending: Phase 30, v1.3]`.** Present it as roadmap, shown via a mock or slide, not as a live feature.
- Narrate: "At install, the box inspects its own hardware (RAM, VRAM, NPU class) and recommends the right model and quantization tier so the operator never hits an out-of-memory wall on first run. On a DGX Spark, 128GB unified memory, it recommends a 70B-class model at Q4."
- **Presenter note:** Say "this is on our v1.3 roadmap" explicitly. Show the advisor as a wireframe or slide. Do not run it as if it exists.

**Beat C, the pitch (30s).**
> "A bank in Dhaka cannot send customer data to a US cloud. A government ministry cannot either. Regulation and sovereignty rule out every foreign API. EnterpriseEdge is their AI, on their server, in their building: the same OpenAI-compatible API our cloud serves, with zero data egress. When RTX Spark workstations ship this fall, the hardware to run this sits on a desk for under five thousand dollars."

**Fallback plan:** Pre-record a GIF of `docker compose --profile enterprise up` reaching healthy (`/health` 200 on edge plus control-plane). The hardware advisor is a slide regardless, with no live dependency, so no GIF is needed; just present the wireframe.

---

## 5. Business (2 min)

**Goal:** Show the money rails are real, the burn is tiny, and the path is mapped.

**Beat A, prepaid BDT rails, live (40s).**
- Show the developer console billing page (or a recorded flow) with the three rails: **bKash, SSLCommerz, Stripe**.
- Narrate: "Prepaid credits. Three payment rails, all live and shipped in v1.0. FX is computed with arbitrary-precision math, not floats, so credit accounting never drifts."
- **Regulatory note (on stage):** For BD customers we never display FX rates or exchange language; `amount_usd` is omitted from BD payment responses. Mention this as a compliance strength, not a limitation.

**Beat B, burn (20s).**
- Narrate: "We have no hardware bill and no idle cloud. The demo runs on a dev machine; the cloud stack is lean Go on managed infra. Burn is under fifty dollars a month today. Funding buys hardware and go-to-market, not survival."
- **Presenter note:** The under-$50/month figure reflects today's pre-funding posture (dev machine plus managed Supabase/Redis, free-tier models), per MVP owner answers. Frame it as capital efficiency.

**Beat C, roadmap board (40s).** Show the roadmap slide:

| Milestone | Theme | Highlights |
|---|---|---|
| **v1.0** (shipped) | Developer API core | OpenAI-compatible `/v1`, BDT billing, 3 payment rails |
| **v1.1** (in progress) | Chat app plus provider catalog | Open WebUI chat, Bangla locale, provider CRUD, tools capability |
| **v1.2** | Agentic surface | Anthropic Messages API, MCP connectors, router-LLM intelligent routing |
| **v1.3** | Device era | Hardware detection plus model advisor, mobile and desktop apps with on-device router-agent, **DGX Spark / RTX Spark class** self-host |

- Narrate the arc: "Today, the developer API and chat. Next, the agentic surface so coding agents and MCP tools run on us. Then the device era: your AI on a workstation-class box you own."

**Beat D, the ask (20s).**
- **Slide: ask placeholder.** `[Ask: $___ for ___ months runway, allocated to hardware (DGX/RTX Spark class), BD go-to-market, and EnterpriseEdge pilots with banks, telco, and government]`.
- Narrate the ask in one sentence, then stop talking.

**Fallback plan:** Pre-record a GIF of the console billing page showing the three rails. Roadmap and ask are slides, with no network dependency.

---

## 6. Q&A prep: 10 hard questions, honest answers

1. **"OpenRouter already gives an OpenAI-compatible API. Why do you exist?"**
   OpenRouter takes USD cards. We take bKash and SSLCommerz, settle in BDT, and run a sovereign self-host SKU OpenRouter has no answer for. The wedge is payments and data residency in a 170M-person market, not raw model access.

2. **"What stops OpenAI or a global player from localizing payments tomorrow?"**
   Nothing technical, which is exactly why we move now. Our moat compounds in the enterprise self-host story (EnterpriseEdge), local payment integrations, and Bangla product depth, none of which a global player prioritizes for one market. We're racing the localization window deliberately.

3. **"What's the moat once the API is commoditized?"**
   Three layers: (a) BDT payment rails and compliance that take months to replicate per-market; (b) EnterpriseEdge, banks and government buying a box, a sales motion with switching costs; (c) the device era, an on-device router-agent and model advisor tied to specific hardware we'll support first.

4. **"Unit economics. You resell models you don't own. Where's the margin?"**
   Prepaid credits with a spread on inference, plus EnterpriseEdge licensing (per-box, recurring) where there's no per-token COGS to us at all. Cloud is volume-thin-margin; EnterpriseEdge is the high-margin enterprise line. We make `math/big` FX precise specifically so the spread never leaks to rounding.

5. **"Why now?"**
   Two clocks. Global players haven't localized BD payments yet (consumer and developer window). And DGX Spark just hit retail at $4,699 with RTX Spark class shipping this fall (enterprise self-host window). Both windows are open today and close within 12 to 18 months.

6. **"How much of this is real versus roadmap?"**
   The developer API, BDT billing across three rails, and the chat app are shipped and live. Provider catalog and tools capability just merged. Pending and clearly marked: Bangla UI staging deploy (Phase 25), tool passthrough wire (Phase 20-05), and the hardware advisor (v1.3). We don't hide the line between shipped and planned.

7. **"You have no hardware. Isn't EnterpriseEdge vaporware?"**
   The software stack runs today via one compose profile: gateway, chat, router, optional local model. What we don't own yet is the DGX/RTX Spark box, which is a $4,699 purchase post-funding, not an R&D risk. The advisor and installer polish are scoped in v1.3. We're buying hardware with the round, not inventing it.

8. **"bKash and SSLCommerz integrations. Are they certified and compliant?"**
   The rails are integrated and shipped in v1.0. For regulated specifics (settlement, KYC, data protection under local law) we follow the relevant payment-provider and regulatory requirements and consult counsel before scaling volume. We treat compliance as a gating dependency, not an afterthought.

9. **"Provider lock-in. What if OpenRouter or Groq cuts you off?"**
   The gateway is provider-agnostic by design; the catalog supports custom providers (merged PR #197). We route across OpenRouter and Groq today with fallbacks, and can add any OpenAI-compatible provider, including a customer's own EnterpriseEdge models, without code changes.

10. **"What happens to your cloud margin when on-device models get good enough to replace the API?"**
    That's our v1.3 thesis, not a threat. We're building the on-device router-agent and model advisor ourselves. As local models improve, the router keeps the cheap and private calls on-device and sends only what needs a frontier model to the cloud. We monetize the routing and the box, so the trend we'd supposedly fear is the product we're shipping.

---

## Appendix: exact commands reference

```bash
# Health check (pre-flight)
curl -s https://api-hive.scubed.co/health

# Raw chat completion, free model (Section 3, Beat A)
curl https://api-hive.scubed.co/v1/chat/completions \
  -H "Authorization: Bearer $HIVE_KEY" \
  -H "Content-Type: application/json" \
  -d '{"model":"llama-3.3-70b","messages":[{"role":"user","content":"Say hello to our investors in one sentence."}]}'

# EnterpriseEdge self-host (Section 4, Beat A)
cd deploy/docker
docker compose --env-file ../../.env --profile enterprise up --build

# Chat workstation local fallback (Section 2)
docker compose --env-file ../../.env --profile enterprise --profile chat up --build
# Open WebUI via Caddy at http://localhost:8090
```

> **Placeholders:** `<HIVE_KEY>` and `$HIVE_KEY` are demo credentials injected at runtime, never committed and never shown on screen. No real keys, account ids, or customer data appear anywhere in this script or in the live demo.
