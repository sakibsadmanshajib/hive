# Hive demo guide

One page for bringing up and presenting the Hive demo. Audience: BD prospects and Enterprise buyers, presented by the owner, five concurrent users max on the box. Companions in the project vault: the Claude parity audit (`session-2026-08-24-claude-parity-audit.md`) and the walkthrough script (`walkthrough-demo-2026-08-24.md`).

## Live surfaces

| Surface | URL | Role |
|---|---|---|
| Chat | https://chat-hive.scubed.co | Product shell: chat, voice, code execution, knowledge, artifacts |
| Console | https://console-hive.scubed.co | Operator and developer surface: API keys, billing, catalog, logs, analytics, members, feature gates, marketplace |
| API | https://api-hive.scubed.co/v1 | OpenAI-compatible gateway base |
| Control plane | https://control-hive.scubed.co | Tenant and admin control API |

The demo box deploys continuously from main. Anything merged is live minutes later; anything unmerged does not exist on stage.

## The free pool (hive-free)

`hive-free` is the free tier serving alias. One alias pinned to a route group of four provider routes that share a single LiteLLM deployment name, so LiteLLM's router balances load across them:

| Member | Upstream | Provider key env var |
|---|---|---|
| route-free-pool-free | OpenRouter dots-3-note-preview:free | `OPENROUTER_API_KEY` |
| route-free-pool-gemini | gemini-flash-latest via Google's OpenAI-compatible endpoint | `GEMINI_API_KEY` |
| route-free-pool-groq | Groq gpt-oss-20b | `GROQ_API_KEY` |
| route-free-pool-groq-2 | Groq gpt-oss-20b | `GROQ_API_KEY_2` |

Automatic failover: an exhausted or failing key is cooled down by the router (3 failures, 30 second cooldown) and traffic moves to the surviving members, so one dead key never takes the alias down. Every upstream costs nothing; the alias is priced as a service at 0.001 USD input and 0.004 USD output per million tokens.

Two repoints shipped alongside the pool (PR #1115): `hive-auto` is now the real OpenRouter Auto Router billed at actual upstream cost, and `hive-default` moved to deepseek-v4-flash as the paid quality tier with verified tool support. CI and daily automated consumption run on `hive-free`, so testing never spends the demo budget (#1097).

Stated honestly: `hive-free` rejects tool bearing requests at selection time (tools_supported is false until cross member parity is probed). Tools and structured output belong to `hive-default` and `hive-auto`. Reasoning works on the pool.

## The 30 minute script

| Min | Beat | Notes |
|---|---|---|
| 0 to 3 | Sign in via OIDC, land on one shell | The OAuth consent round trip works end to end |
| 3 to 8 | Chat: strong model, Bengali plus English, voice dictation | Streaming works; dictation is metered like every other call; picker labels are still opaque aliases (#941) |
| 8 to 14 | Retrieval: grounded question over your own document | The chat Knowledge surface is dead today (#1109), so demonstrate `/v1/rag/chat` with curl against api-hive instead |
| 14 to 20 | Artifacts: generated content opened on its hosted link | The chat artifacts index spins forever (#1110), so fall back to an API published static artifact |
| 20 to 26 | Developer story: mint an API key in the console, call api-hive with an OpenAI SDK, show usage in analytics | A freshly minted key can 403 on first use outside the fixture account (#798); rehearse with the demo fixture key |
| 26 to 30 | Money story: prepaid credits, ledger, spend caps | Look only: credits and caps are viewable, live checkout stays off stage while #917 and #928 are open, pre made slide as fallback |

Cowork: the agent service gate now defaults on for every workspace (#1107 closed by PR #1111) and code interpreter genuinely executes inside chat. If a live sandbox launch misbehaves on the day, show the code interpreter beat and move on.

## What works

- Sign in through console OAuth consent into one chat shell
- Streaming chat including Bengali, voice dictation and read aloud on the metered gateway path (read aloud wired by PR #1079)
- Code interpreter inside chat: sandboxed execution rendered as inline tool cards
- Uploads with visible size caps and honest inline errors (#1108 and #1113 fixed)
- Full console: keys, catalog, logs, analytics, members invites, feature gates, MCP marketplace. Credits are viewable in billing (balance plus ledger); the live top up flow is shown only if #917 and #928 close by demo day, otherwise a pre made slide stands in
- Free pool with automatic cross provider failover
- RAG over the API (`/v1/rag/chat`) with admin selectable embedding model and dimension

## Known rough edges (open issues)

- Knowledge nav item dead on chat (#1109)
- Artifacts index renders an eternal loading shell (#1110)
- Model picker offers opaque aliases with no purpose subtitles (#941)
- Sidebar still carries Agents, Knowledge and Folders pending the D-045 rebuild (#944); Scheduled just landed behind its own surface (PR #1118)
- No credits or usage visibility anywhere in chat (#1063)
- Freshly console minted keys can 403 on first real use (#798)
- Streamed turn settlement edge cases make live checkout undemoable (#917, #928)
- Verification tooling can leave admin grants on the demo account (#752) and conversations accumulate on it (#916)

Not demoable: multi user isolation claims (#947, #948, #949 family), Enterprise sovereignty claims, live checkout.

## T-1 day checklist

1. Confirm deploy-demo-box is green on main and the box serves the merge SHA. Post-deploy verification gates the deploy since PR #1061; presenting against an undeployed main is the classic silent failure.
2. Confirm the OWUI nightly e2e ran within the last 24 hours; trigger it manually otherwise.
3. Run `scripts/post-deploy-verify.py` against the box and confirm backend specific health for all four hosts (for chat that means `/api/config` reporting status true with the OIDC provider configured). A bare root HTTP 200 from chat-hive, console-hive, api-hive or control-hive .scubed.co is not readiness evidence.
4. Clean the demo account: no leftover admin grants (#752), conversations pruned (#916), credits topped up, and the account's Enter key behavior verified with `node apps/web-console/scripts/demo-chat-settings.mjs <demo account email>` (add `--repair` if it drifts).
5. Confirm last night's backup set verified. Never demo the day backups are red.
