# Coding-Agent and Cowork Subsystem: Design Specification

**Status:** Decisions documented (owner-approved). This is a documentation-of-decisions record, not a fresh design proposal. Every decision below was made and ratified during the July 2026 design discussions; this document captures them with traceable evidence so the owner can verify each against its source issue.

**Date:** 2026-07-07
**Amended:** 2026-07-07: one-product/two-mode frame ratified by owner (original date retained)
**Author:** Spec writer (orchestrator synthesis)
**Epic:** [#305](https://github.com/sakibsadmanshajib/hive/issues/305)
**Sub-issues:** #306, #307, #308, #309, #310, #311; related build items #300, #312, #304, #292, #293
**Supersedes / extends:** `.planning/carl/CHARTER.md` and `.planning/carl/DESIGN.md` (2026-06-25) where noted in Section 11.

---

## 1. Overview and goal

The Hive Enterprise sovereign-edge line adds a single agent subsystem that covers two user-facing personas on one shared engine:

1. A coding agent (git, build, test, lint work).
2. A knowledge-work cowork agent (document read and write, RAG corpus search, slides and deck generation, document-layout understanding, an Artifacts-style code preview).

Both personas are plugin packs over one shared agent engine rather than two separate engines (#305). The subsystem runs in two execution environments, a server-side web surface and a native desktop app, sharing one policy source of truth for network egress and one distribution mechanism for extensions. The whole subsystem is GUI-only by owner decision (#305).

The subsystem must uphold the product's core promise, "your data never leaves your box." Section 3 documents where the currently shipped code falsifies that promise and how this build restores it.

---

## 2. Context and business rationale

### 2.1 The sovereignty promise, and where shipped code currently breaks it

Hive Enterprise is sold as a fully self-hosted alternative to OpenAI and Anthropic, "zero external SaaS dependency and zero external API keys at the edge" (`.planning/carl/CHARTER.md`, locked decision 2). Three findings from the July 2026 gap review show the shipped code does not yet hold that line. This subsystem's model and egress dependencies restore it:

- **Sovereign guard is dead code.** `HIVE_SOVEREIGN` is written to `.env` by the installer but no service reads it at runtime, and no compose file injects it into any container, so the guard reads an empty string and never fires on shipped enterprise boxes. The guard is also narrow: it checks only `OPENROUTER_API_KEY` and `GROQ_API_KEY`, so any other provider key, a DB-managed custom provider endpoint, or any of the six external audit sinks passes straight through (#245).
- **RAG leaks documents externally.** Open WebUI's chat RAG embeds every uploaded document via OpenRouter's `text-embedding-3-small`, because `deploy/litellm/config.yaml` has no local embedding route. Document chunks leave the box on every upload, on a product sold as zero external egress. Confirmed independently twice (#295, #232 comment).
- **No local inference exists.** Local LLM inference is entirely greenfield: `deploy/litellm/config.yaml` has only a disabled Ollama stub comment, and no vLLM or SGLang exists anywhere in the repo (#294).

These are the specific, evidenced falsifications. The claim is not that the whole product is non-sovereign; it is that these three seams must close for the agent subsystem to be truthfully sovereign.

### 2.2 Hardware-agnostic posture

The subsystem never targets one hardware profile. The local-inference ladder auto-detects and selects a backend by hardware class: vLLM primary on NVIDIA CUDA and AMD ROCm, SGLang for AMD Instinct MI300X, llama.cpp for the thin, CPU, or consumer AMD tier, with Ollama explicitly dropped (#294). Deployment tiers span DGX-class hardware down to consumer RTX and CPU-only boxes (#178). The installer's existing hardware advisor drives model selection (`.planning/carl/DESIGN.md` section 6).

### 2.3 Chat client baseline

Open WebUI is the permanent v1 chat client. The 2026-06-25 charter had named Lobe Chat as the shipped shell; on 2026-07-06 the owner made the final call to accept Open WebUI's post-2025 branding-retention clause as a knowing documented risk and keep Open WebUI, which is the real shipped client already wired into the compose profiles (PR [#291](https://github.com/sakibsadmanshajib/hive/pull/291)). The agent surface therefore extends Open WebUI first (Section 4.7). Note: PR #291 records this decision but is still open at time of writing (see Section 12).

---

## 2A. Product frame: one product, two deployment modes

The owner ratified this frame on 2026-07-07. Hive is one product with one codebase, switched at runtime into one of two deployment modes.

- **Hive** (cloud): us-hosted, uses external cloud models, with some enterprise features gated off.
- **Hive Enterprise** (customer-hosted): for sensitive industries, running on the customer's own hardware.

There is no fork. Feature differences between the two modes are decided per feature gate, not by a fixed capability list. (Gap: today there is no per-deployment-mode gate default layer sitting above the per-tenant gates; that default layer is a backlog item.)

**Three inference postures:**

1. **Cloud:** external cloud providers.
2. **Enterprise default:** local models by default, with an admin opt-in to add external provider keys knowingly.
3. **Enterprise strict-sovereign lockdown:** the shipped `HIVE_SOVEREIGN` guard (#245) becomes an optional toggle rather than an always-on default, for buyers who need a provable air gap.

This agent and cowork subsystem ships in BOTH modes. It is sovereign-first: where a tradeoff conflicts, the enterprise posture wins and cloud follows via gate flips. CI may exercise external models at test time only; an external model is never a runtime dependency in either mode.

**Entitlement principle.** One runtime entitlement gate resolves identically in both modes, over two transports: a cloud sync path, and a signed offline license file (#304).

**Admin plane inheritance.** The agent subsystem gets no separate admin console. It inherits the Hive admin plane (feature gates, egress policy, marketplace curation). Policy travels with identity: a desktop sign-in pulls the tenant's gate state and license state (#310), so the same policy applies wherever the user signs in.

**Tenancy ruling.** A single organization is a single tenant. Departments are separated by RBAC inside that one tenant, not by additional tenants. The multi-tenant schema present throughout the codebase exists for extensibility only; most Hive Enterprise deployments run single-tenant. Per-tenant language in the sections below (per-tenant quota, per-tenant feature gates, multi-tenant isolation testing) should be read against this ruling: on a typical single-tenant enterprise box those same mechanisms scope per user and per department inside the one tenant.

**Hive Dedicated.** A us-hosted single-tenant variant under Hive Enterprise (the GitLab Dedicated pattern) is shelved for now, tracked in #313.

---

## 3. Locked decisions with evidence

Each row is an owner-approved decision. The citation is the issue (and comment where the decision was appended) that records it.

| # | Locked decision | Evidence |
|---|-----------------|----------|
| D1 | One shared agent engine: OpenHands (MIT), excluding its `enterprise/` directory which carries a separate license. Two plugin packs (coding-pack, knowledge-work-pack) defined as OpenHands microagent configs, built and shipped in parallel. | #305 |
| D2 | Identical sandbox trust level for both packs: both run at the same sandbox tier, arbitrary shell, build, and test commands allowed in both, matching how the owner uses Claude Cowork today. | #305 |
| D3 | Execution split by surface. Web: server-side execution on Apptainer rootless, plus an in-house per-tenant quota layer, plus an `allowed_hosts` egress allowlist. Desktop: local execution using native OS-primitive sandboxes forked and vendored from OpenAI Codex's Apache-2.0 sandbox crates, not hand-written. | #308 (server), #306 (desktop) |
| D4 | Interface is GUI-only. No CLI agent and no headless automation surface. Both are explicitly rejected, not backlog. No Bugbot-style PR-review agent either. CLI-style users point the real Claude Code CLI at the already-shipped Anthropic-compatible `/v1/messages` surface. | #305 comment (2026-07-07) |
| D5 | Agent surface UI extends the Open WebUI codebase first. The Tauri desktop shell adapts to it afterward, not the reverse. | #311 comment (2026-07-07, DECIDED) |
| D6 | Egress policy is a single source of truth in control-plane, admin-configurable per user (not only per tenant), consumed by both the server-side workspace config and the desktop firewall rules. | #308 |
| D7 | Extensions distribution is two-tier: an admin-curated baseline available on the web and server path, plus user-added local or external servers allowed on desktop only with more leniency. One distribution mechanism covers MCP servers and rules, skills, and prompt templates together (Cursor Team Marketplace pattern folded in), not two systems. | #309 + comment |
| D8 | Task portability: web-started tasks sync everywhere including desktop; desktop-local-only tasks do not sync to cloud by default; the desktop prompts per task whether to run locally or against the server. | #311 (owner direction, not locked; residual confirmation open, see Section 10) |
| D9 | Licensing is decoupled from feature gates. A license carries duration, tier label, and seat or user count only, validated from an offline signed file on a schedule with no phone-home (NVIDIA Delegated License Server pattern). Feature gates remain the sole capability on-off mechanism. Tier-based restriction of which gate keys an admin may enable is a designed-but-deferred extension point: build the seam, do not build the enforcement. | #304 + comment |
| D10 | Claude Design folds into the knowledge-work-pack as a slides and deck skill. No separate subsystem. | #300 |
| D11 | Artifacts hosting: store a self-contained HTML blob in existing Supabase Storage, serve it at a persistent versioned URL via edge-api, render inside a sandboxed iframe under a strict CSP on an isolated subdomain, share private-by-default via existing RBAC. Small build, no new dependency, no CDN. | #312 |
| D12 | Desktop app authenticates against the edge box using the same GoTrue or JWT flow used elsewhere, then fetches the tenant's feature-gate state and license or seat state before enabling any plugin pack locally. | #310 |
| D13 | A security validation spike must run and pass before any implementation work begins on the subsystem. | #307 |

---

## 4. Per-subsystem design

### 4.1 Shared engine and the two plugin packs (#305)

One engine, OpenHands (MIT), is the substrate. Its `enterprise/` directory is excluded because it carries a separate license. The two personas are not two engines; they are two OpenHands microagent configurations:

- **coding-pack:** git, build, test, and lint tooling.
- **knowledge-work-pack:** document read and write, RAG corpus search, and the skills in Section 4.8.

Both packs ship in parallel (owner decision) and both run at the same sandbox trust tier: arbitrary shell, build, and test commands are permitted in each, matching the owner's own use of Claude Cowork. There is no elevated versus restricted split between the packs (#305).

### 4.2 Execution split (#306, #308)

Execution differs by surface, not by pack.

**Web (server-side):** the agent executes on the shared edge box inside Apptainer rootless. OpenHands ships no per-tenant resource quota or noisy-neighbor control, and an upstream bug already exists where one user's heavy load crashed other tenants' agents on a shared box, so Hive builds its own quota layer on top of the Apptainer rootless backend (#308). Cross-tenant isolation is required because Hive cloud mode is multi-tenant; on single-tenant Hive Enterprise deployments the same quota and isolation boundary applies per user and per department (RBAC), so the design carries over unchanged. Network access is bounded by the OpenHands `allowed_hosts` workspace config, populated from the egress policy in Section 4.4.

**Desktop (local):** the agent executes on the user's own machine inside native OS-primitive sandboxes. The owner has explicitly stated they will not hand-write untrusted security-critical isolation code and prefer adopting proven open source, so the desktop backends are forked and vendored from OpenAI Codex's Apache-2.0 sandbox implementation rather than written from scratch (#306). Codex's crates cover all three platforms with native OS-primitive isolation:

- **Linux:** bubblewrap plus user namespaces plus Landlock LSM plus seccomp-BPF. The app bundles a static bubblewrap binary (roughly one megabyte, no daemon).
- **macOS:** sandbox-exec (Seatbelt) profile, built into the OS, the same mechanism Claude Code uses, zero setup.
- **Windows:** native OS-primitive isolation, no virtual machine. Elevated mode uses dedicated low-privilege sandbox users plus filesystem permission boundaries plus Windows Firewall deny-outbound rules; the unelevated fallback uses a restricted token plus ACL boundaries plus Job Objects. This works on Windows Home with no Virtual Machine Platform or Hyper-V dependency, unlike Claude Desktop's Cowork VM approach.

These are built against OpenHands' documented `workspace_factory` plugin point. This is a fork-and-adapt job, not a crates.io dependency add, because the Codex crates are workspace-internal and coupled to Codex's own exec-server (#306 comment). Five alternative Rust sandboxing crates were checked (birdcage, gaol, landlock, extrasafe, hakoniwa); one is GPL-blocked and the rest are Linux-only or have unimplemented Windows support, so the Codex crates are the only maintained OSS covering all three platforms with native Windows isolation (#306 comment).

### 4.3 Security validation spike (#307)

Before any implementation on #305, #306, or #308 through #311, a security spike must confirm the isolation holds. The sandbox approach itself is decided (Section 4.2); this is validation of a decided approach, not an open design question (#307 comment). The spike must:

- Confirm Apptainer holds multi-tenant isolation under adversarial testing on the shared server path.
- For every desktop sandbox backend, confirm that no file the sandboxed agent can write is later executed or read by an unsandboxed host process. This is the Configuration-Based Sandbox Escape (CBSE) pattern disclosed in 2026 that hit OpenAI Codex CLI, Claude Code, and Gemini CLI: a hook or config the agent could write from inside the sandbox executed outside it on each agent turn, achieving host code execution while the sandbox looked intact.
- Confirm the Docker socket is never exposed to the agent process on the server path, since OpenHands maintainers describe Docker socket access as effectively root on the host.

### 4.4 Egress policy: single source of truth (#308)

Some users need internet access (web search, package downloads, documentation lookups) and some must not. Egress is therefore admin-configurable per user, not only per tenant. One egress allowlist configuration lives in control-plane as the single source of truth. It is consumed by both the server-side OpenHands `allowed_hosts` workspace config and the desktop local firewall rules, so the same policy applies on every surface rather than drifting between them the way the `HIVE_SOVEREIGN` guard already has (#308, #245).

### 4.5 MCP and rules or skills marketplace (#309)

Two tiers, one mechanism:

- **Admin-curated baseline:** an admin-vetted marketplace of MCP servers available on both web and server paths, similar to the official connector lists Claude and Codex curate.
- **User-added, desktop-only:** looser user-added local or external MCP servers allowed specifically on the desktop path, where the risk is scoped to the user's own machine rather than shared tenant infrastructure.

One distribution mechanism covers MCP servers and rules, skills, and prompt templates together, folding in Cursor's Team Marketplace pattern (v3.10, June 2026), rather than building two separate systems (#309 comment).

### 4.6 Desktop auth and gate or license fetch (#310)

The desktop app has zero awareness of Hive's control-plane unless explicitly wired, the same blind spot found in Open WebUI during the featuregate audit. On startup the desktop app authenticates against the edge box using the same GoTrue or JWT flow used elsewhere, then fetches the tenant's feature-gate state and the license or seat state before enabling any plugin pack locally (#310).

### 4.7 Task portability and agent UI panel placement (#311)

**UI panel placement (decided):** the agent surface is not chat-embedded. The panel extends the Open WebUI codebase first, and the Tauri desktop shell adapts to it afterward (#311 comment, DECIDED).

**Task portability (owner direction):** web-started tasks sync everywhere including desktop; tasks run in the local desktop environment do not sync to the cloud by default; the desktop prompts per task whether to run locally or against the server (#311). The issue flags that this direction still needs a closer look at how Claude Cowork and Codex handle portability before it is committed; that residual check is listed in Section 10.

### 4.8 Knowledge-work-pack skills (#300, #312)

All knowledge-work skills are pure orchestration over routed models plus templates, requiring no new infrastructure (#300). They live inside the knowledge-work-pack:

- **Document-layout VLM route:** contract and PDF understanding via a self-hostable vision model, serving regulated document-heavy buyers.
- **Slides and deck generation:** the Claude Design capability (D10), matching Z.ai's Slide Agent and Anthropic's Claude Design deck capability.
- **Artifacts-style code and preview canvas:** built on Open WebUI's existing Artifacts feature plus a routed model, matching Z.ai's Full-Stack builder. Its hosting mechanism is Section 4.9.

### 4.9 Artifacts hosting (#312)

Open WebUI already provides safe rendering (a sandboxed `srcdoc` iframe in a side panel, strict CSP via `IFRAME_CSP`, configurable sandbox flags) but not a persistent standalone shareable artifact URL. This build adds the missing persistence and sharing, small, with no new dependency and no CDN (#312):

1. Store the self-contained HTML blob keyed by id and version in existing Supabase Storage (the `hive-files` bucket).
2. Serve it at a persistent path via edge-api (Go): `/artifacts/{id}` for latest and `/artifacts/{id}/v/{n}` for a specific version. A same-path redeploy mints a new version at the same URL, matching the Claude Artifacts model.
3. Serve from a separate origin or subdomain with a strict CSP (`connect-src 'none'`, no external resources), `frame-ancestors` restrictions, rendered inside a sandboxed iframe. Origin isolation prevents the artifact from reading the main app's cookies or session.
4. Sharing is just the persistent URL, access-controlled via existing RBAC, private by default with opt-in public, matching the owner-discretionary posture used elsewhere.

### 4.10 Licensing and entitlement (#304)

A license is an offline signed file, validated locally on a schedule, with no phone-home, matching the NVIDIA Delegated License Server pattern and fitting the sovereignty story (#304). It carries three attributes only: support duration, tier label, and seat or user count. It must never gate features directly. Feature gates (#238) remain the sole capability on-off mechanism, fully admin-controlled and independent of the license.

Tier-based restriction of which gate keys an admin may enable is not built now (no immediate need), but both the licensing service and the featuregate admin API must be built generically enough to add it later without a rearchitecture (#304 comment):

- The license service keeps tier as a plain queryable attribute; it never hardcodes "all gates available to all tiers" in a way that cannot be revisited.
- The featuregate admin-toggle API and UI (#292) expose a generic list of available keys plus enabled state, not a fixed enum tied to tier.
- The future extension inserts a tier-eligibility predicate between license lookup and featuregate toggle, additive when it happens, not a breaking change.

This depends on two adjacent featuregate build items: the admin console UI to flip per-tenant gates (#292), and the featuregate data-model rework to move off the hardcoded five-boolean response so new gates (including the Cowork gate, which today has a constant but no route check) can be added without six manual edits across both services (#293).

### 4.11 Sovereignty-restoration dependencies (model suite, sovereign guard)

The agent packs route to models that must be local for the sovereignty claim to hold. These are tracked as their own issues and are dependencies of this subsystem, not part of its own code:

- **Local LLM inference** via the vLLM, SGLang, llama.cpp ladder (#294).
- **Local embeddings** via BGE-M3 (MIT, 1024 dimensions, over 100 languages including Bangla, English, and French) served through vLLM's embedding mode, added as the primary route in `deploy/litellm/config.yaml`, closing the RAG external egress leak. This resolves the CHARTER (bge-m3, 1024) versus DESIGN (nomic-embed, 768) conflict in favor of BGE-M3. Hugging Face TEI is rejected because its license changed to HFOIL, which conflicts with the license clearance gate (#295).
- **Local TTS** via Kokoro-FastAPI (Apache-2.0, native OpenAI-compatible endpoint). Bangla TTS remains an explicit open gap, not to be claimed to clients until resolved (#296).
- **STT wiring:** Parakeet and faster-whisper already exist in compose but are gated to a voice profile and their base-URL env vars are never set, so the path is unreachable; this is pure wiring into the enterprise profile (#297).
- **Self-hosted web search** via SearXNG for chat, giving parity with competitors without external egress (#298).
- **License-clean image generation** via a permissive model such as FLUX.1-schnell behind a thin shim into Open WebUI's image engine, avoiding the copyleft ComfyUI (GPL-3.0) and AUTOMATIC1111 (AGPL-3.0) UIs (#299).
- **Sovereign guard repair:** the `HIVE_SOVEREIGN` guard is an optional strict-sovereign toggle, not an always-on default; the enterprise default posture runs local models with admin opt-in external keys. The repair requirement is that the guard genuinely enforces when enabled: it is currently dead code, and once repaired it must cover all provider keys and DB-managed custom endpoints, be injected into containers, and gate the external audit sinks (#245).

---

## 5. Competitive positioning (rationale only, not build items)

This section informs positioning; it introduces no work items.

- **Cursor's custom endpoint is a chat-panel-only feature.** Cursor's "Override OpenAI Base URL" powers only its chat and plan panel; Composer, inline edit, apply, and Tab autocomplete are hard-locked to Cursor's own backend. A sovereign buyer cannot run Cursor's best agent features on-prem. This is Hive's direct wedge: every surface of both packs must run fully on the customer's own model, treated as a hard requirement (#305 comment).
- **Kimi Work and Z.ai ZCode are real shipping competitors.** Kimi Work (launched 2026-06-12: background agent, local file access, browser control, scheduled jobs, sub-agent swarm) and Z.ai's ZCode (launched 2026-07-02: native coding IDE on GLM-5.2) do roughly what this subsystem designs. The bar is moving fast (#305 comment).
- **DeepSeek and MiniMax have made zero desktop investment** (web, PWA, and mobile only), by contrast (#305 comment).

---

## 6. Anti-scope (permanent, not backlog)

These are permanent exclusions from v1, not deferred tickets, unless noted:

- **No GUI computer-use in v1** (#305).
- **No CLI agent and no headless automation surface.** Explicitly rejected. CLI-style needs are met by pointing the real Claude Code CLI at the shipped `/v1/messages` surface (#305 comment).
- **No Bugbot-style PR-review agent** (#305 comment).
- **No video or music generation.** (See Section 12: no supporting issue was found for this exclusion; it rests on the owner brief.)
- **No mobile app for now.** Deliberately deferred (#305 comment). (See Section 12: a wider-roadmap mobile track does exist as #176 and #178, so this is deferred rather than entirely un-ticketed.)

---

## 7. Open items (documented as open, not resolved here)

- **Tier-based gate-key restriction extension point (D9).** The seam is designed and must be built; the enforcement is deferred until there is real market signal on pricing tiers. Do not build it now, do not remove the seam (#304 comment).
- **Task-portability confirmation (D8).** The owner's direction is documented, but #311 flags that Claude Cowork and Codex behavior should be examined before the portability rules are finally committed.
- **Mobile app deferral.** Considered and deferred, not scoped into this subsystem (#305 comment).
- **Bangla TTS gap.** No permissively licensed Bangla TTS option found; tracked as an explicit open gap, not to be claimed to clients (#296).

---

## 8. Risks

1. **Sandbox escape (CBSE class).** The exact config or hook escape that hit Codex, Claude Code, and Gemini CLI in 2026 is the highest-severity risk. Mitigation: the mandatory pre-implementation security spike (#307) plus vendoring Codex's proven Apache-2.0 isolation rather than hand-writing it (#306).
2. **Multi-tenant isolation on the shared server path.** OpenHands has a known noisy-neighbor crash bug and no built-in quota. Mitigation: the in-house per-tenant quota layer plus adversarial Apptainer isolation testing (#308, #307).
3. **Egress policy drift.** The `HIVE_SOVEREIGN` guard already drifted into dead code. Mitigation: one control-plane source of truth consumed by both surfaces, not per-surface config (#308, #245).
4. **Fork maintenance burden.** Vendoring Codex's sandbox crates couples Hive to an internal, unpublished implementation that will need periodic re-syncing. Accepted because no alternative covers all three platforms (#306 comment).
5. **Sovereignty claim outrunning reality.** Marketing must not claim capabilities the model suite has not yet made local (local inference, local embeddings, Bangla TTS). Mitigation: Section 4.11 dependencies must land before the corresponding claim (#294, #295, #296).

---

## 9. Acceptance posture

This subsystem is not shippable until, at minimum: the security spike (#307) passes on all four sandbox backends and the Apptainer server path; both packs run at the identical trust tier on both surfaces; the egress policy is enforced identically on web and desktop from one control-plane source; the desktop fetches gate and license state before enabling any pack; and the Section 4.11 sovereignty dependencies that back any customer-facing claim are live. Detailed acceptance criteria belong to the individual sub-issues.

---

## 10. References

| Issue or PR | Subject |
|-------------|---------|
| #305 | Epic: coding-agent and cowork on a shared OpenHands engine |
| #306 | Multi-platform native desktop sandbox backends (fork Codex crates) |
| #307 | Security spike: validate sandbox isolation before implementation |
| #308 | Server-side sandbox: Apptainer rootless plus quota plus egress allowlist |
| #309 | MCP and rules or skills marketplace: two-tier distribution |
| #310 | Desktop auth and feature-gate or license fetch at startup |
| #311 | Task portability and agent UI panel placement |
| #300 | Knowledge-work-pack skills (doc-layout VLM, slides and deck, Artifacts) |
| #312 | Self-hosted Artifacts hosting mechanism |
| #304 | Licensing and entitlement, decoupled from feature gates |
| #292 | Admin console UI for per-tenant feature-gate toggles |
| #293 | Featuregate data-model rework off the hardcoded five-boolean response |
| #294 | Local LLM inference: vLLM, SGLang, llama.cpp ladder, Ollama dropped |
| #295 | Local embeddings via BGE-M3 to close the RAG egress leak |
| #296 | Self-hosted TTS via Kokoro-FastAPI; Bangla open gap |
| #297 | Wire Parakeet and faster-whisper STT into the enterprise profile |
| #298 | Self-host web search via SearXNG |
| #299 | License-clean self-hosted image generation |
| #245 | Runtime enforcement of `HIVE_SOVEREIGN` (dead guard repair) |
| #232 | Hive Enterprise RAG: pgvector schema, local embeddings, endpoints |
| #178 | On-device capability suite (v1.3, cross-referenced, not duplicated) |
| #221 | Hive Enterprise hardening for regulated buyers |
| PR #291 | Open WebUI confirmed as permanent v1 chat client |
| #168, #243 | Anthropic-compatible `/v1/messages` surface (design; shipped) |

---

## 11. Supersession notes (charter and design evolution)

The 2026-06-25 charter and design predate this subsystem's decisions. Recorded for the record:

- **Coding agent.** CHARTER decision 7 deferred the coding agent ("The OpenCode coding agent is deferred and is not the main target"). Issue #305 supersedes this: the coding agent is now a real v1 subsystem built on OpenHands, not OpenCode.
- **Cowork meaning.** DESIGN section 5 defined "co-work workspace" as a multi-user shared chat and RAG demo. This spec's "cowork" is the OpenHands knowledge-work-pack agent, a different and broader meaning. The older multi-user shared-chat sense is not what this document scopes.
- **Web client.** CHARTER meeting-1 named Lobe Chat; superseded by Open WebUI (PR #291, owner final call 2026-07-06).
- **Embeddings.** DESIGN proposed nomic-embed at 768 dimensions on Ollama; superseded by BGE-M3 at 1024 dimensions on vLLM (#295), consistent with Ollama being dropped (#294).

---

## 12. Discrepancies between the drafting brief and the source issues

Flagged transparently so the owner can adjudicate:

- **PR #291 merge state.** The brief describes Open WebUI as the permanent v1 client "per merged PR #291." At time of writing PR #291 is open, not merged. Its body records the owner's 2026-07-06 final call, so the decision is real; only the merge status differs from the brief.
- **Task portability is owner direction, not fully locked.** The brief lists D8 as a locked decision. Issue #311 presents it as the owner's "current direction" and states it "needs a closer look at how Claude Cowork and Codex actually handle this before committing," and the DECIDED comment on #311 resolved only the UI panel placement, not portability. Documented as owner direction with a residual open confirmation (Section 10).
- **No video or music generation anti-scope has no issue basis.** The brief lists "no video or music generation" as permanent anti-scope. No issue in the reviewed set (#292 through #312, plus #245, #232, #178, #172, #221) mentions video or music generation in any form. Image generation is covered (#299), video and music are not. This exclusion rests on the owner brief alone; flagged verbatim per instruction.
- **Mobile app is deferred but is ticketed on the wider roadmap.** The brief says mobile is "deferred deliberately, not ticketed." #305's comment defers it, but #178 (and the referenced #176) show a mobile and on-device track already exists on the wider roadmap for v1.3. Mobile is therefore not scoped into this subsystem, but it is not entirely un-ticketed.
- **`/v1/messages` issue number.** The brief cites #243 for the shipped Anthropic-compatible surface. #305's body cites #168 (the surface design) and its correction comment cites #243 (shipped). Both refer to the same surface; #168 is the design issue and #243 is the shipping PR. Not a contradiction, noted for precision.
