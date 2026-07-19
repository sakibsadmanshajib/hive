# Cerebrum (rebuilt 2026-06-12 after .wolf wipe; prior file lost, key lessons reconstructed)

## Do-Not-Repeat
- Agents NEVER run git clean, git reset --hard, or any destructive git op on the shared checkout. .wolf/ is untracked and was wiped by exactly this (2026-06-12 incident).
- Orchestrator-only is strict: main agent never edits code/docs, commits, resolves threads, deploys. Memory files and ledgers only.
- planner/architect/Explore agents are read-only, never assign writes.
- Builder self-reports are not verification: independent reviewer reads the pushed diff (caught phantom triggers in #197, 13 installer defects, 4 litellm sync bugs).
- git checkout -q of a branch checked out in another worktree fails silently; commits land on wrong branch; push with HEAD:<branch> and verify ls-remote.
- Editing an already-applied migration does nothing on the live DB; new migration file always.
- Supabase edge functions with custom header auth deploy with --no-verify-jwt (CLI via npx; project ref in CLAUDE.local.md; migration history has ghost entries, use db query -f workaround).
- haiku only for watch loops/single-shot; sonnet default; opus for design/security/quality-critical.
- Premature completed notifications happen; verify ground truth before respawn; >20min wait-state silence = dead.
- One thread-clearing agent per PR with tight read budget.
- Infer obvious details from data (hive.sqv.co was hive.scubed.co); ask only when ambiguous AND consequential.
- HYGIENE: no IPs, personal emails, tokens, project refs, PII in the public repo OR public PR/issue comments.
- Skipped-by-path-filter required checks BLOCK merges (June 2026 GitHub docs); ci-noop.yml companion workflow exists for docs-only PRs.
- Cloudflare: never the first account in /accounts list; the Hive account id is in CLAUDE.local.md. Turnstile widget hive-signup covers localhost plus hive.scubed.com.bd plus scubed.com.bd.
- E2E fixture CI hits the DEPLOYED edge function; merging supabase/functions changes requires explicit deploy.
- Owner mandates: validate every claim against live sources before deciding (Context7 plus web); user-facing chat caveman ULTRA, thinking and subagents wenyan-ultra; decisions logged with reasoning, owner veto.

## Standing facts
- Budget <$50/month total (CTO operating budget), CI inference cap $5/month, repo PUBLIC.
- Cost baseline 2026-06-11: OpenRouter $10.00 credits, $3.12 lifetime usage. Groq console-only (owner to report). NIM free tier. No Anthropic key.
- Staging: api-hive + cp-hive .scubed.co proxied via Cloudflare (Full strict, Always Use HTTPS), Caddy LE certs on VM, HTTP-01 renewal works through proxy. VM specifics in CLAUDE.local.md.
- Website: hive.scubed.co on Cloudflare Pages (project hive-website), deploy pending.
- Owner pending: bn-BD upstream PR (tools/i18n/openwebui-bn-BD/README.md), Groq spend number, rotate CF global key.
- Operating contract committed: .claude/rules/orchestrator.md. Goal file: .wolf/GOAL.md.

## Carl.sh design (2026-06-25, .planning/carl/DESIGN.md authored)
- Sovereign edge product = gap-close on hive repo. Edge box primary, cloud demo later. Doc validated all tech vs live sources.
- Anthropic /v1/messages (#168) = pure translator, NOT a second path. Reuses inference/handler.go ServeHTTP + chat/dispatch.go + auth.Selector. New pkg apps/edge-api/internal/anthropic/. stop_reason/content-block/SSE mapping validated vs Anthropic SDK (Context7).
- The dispatch seam: new dialect = one case in inference/handler.go ServeHTTP + one translator. Never rebuild dispatch.
- RAG: pgvector NOT yet in repo (no CREATE EXTENSION, no vector col anywhere). New migration vector(768), HNSW vector_cosine_ops m=16 ef_construction=64. Local embeddings via Ollama (nomic-embed-text 768), OpenRouter test/cloud only. New internal/rag pkgs edge+control-plane. pgvector limits: vector<=2000d, halfvec<=4000d (validated).
- Audio: /v1/audio/transcriptions route ALREADY exists in internal/audio/handler.go (RoutingInterface SelectRoute NeedSTT), no STT backend wired. Parakeet = sidecar speaking OpenAI Whisper API; default achetronic/parakeet (Go+ONNX, port 5092, CPU-capable), NVIDIA Riva NIM (port 9000, GPU) as upgrade. Wire via LiteLLM mode:audio_transcription + catalog seed. NO edge handler needed.
- Parakeet-tdt-0.6b-v3 = 25 langs ALL EUROPEAN, NO Bangla. #178 Bangla = TEXT only (qwen3:8b) for v1; Bangla STT deferred behind same sidecar contract. Do not claim Bangla ASR via Parakeet.
- Relay: default LAN via Caddy. Remote port-less = optional Headscale w/ embedded DERP (derp.server.enabled, STUN udp/3478 + HTTPS tcp/443), fully self-hosted. Raw WireGuard rejected as default (no coordinator, cannot hole-punch CGNAT). Installer flag --with-relay.
- Co-work workspace = Open WebUI shell + shared tenant RAG (RLS on tenant_id), container-first. Daytona/OpenCode deferred.
- Build waves: A(Anthropic) B(RAG) C(Parakeet) D(Relay) all parallel-independent; E(installer flags) needs B/C/D; F(co-work) needs A+B; G(E2E). Critical path A+B; C+D short, land first.
- OPEN OWNER DECISION (blocking): edge data-plane sovereignty. Repo assumes Supabase-hosted Postgres+S3. Zero-SaaS edge needs LOCAL Postgres+pgvector + local object storage in enterprise box. Else RAG vectors + docs leave customer server, breaking core pitch. Confirm before RAG build.

## Losses from wipe (unrecoverable)
- buglog.json (~300 entries), anatomy.md, full memory.md history, OPENWOLF.md scaffold. Regenerate scaffold via openwolf init when convenient; buglog restarts empty.

[2026-06-12] code-review-graph uninstalled, replaced by graphify. Use graphify query/path/explain against graphify-out/; rebuild via graphify update (post-commit hook installed). mcp__code-review-graph__* tools no longer exist.
- OWNER ACTION (2026-06-12): Cloudflare scubed.co zone (id fbe80ca310492206988c6fa6d5eb0622) free plan. To prevent recurring error 1010 on api-hive/cp-hive (Browser Integrity Check / Bot Fight Mode blocks non-browser API clients), confirm both OFF in dashboard (Security > Settings > Browser Integrity Check; Security > Bots > Bot Fight Mode). Current CLOUDFLARE_API_TOKEN has zone:read only, cannot script it. Global key correctly removed from .env (rotated). API hostnames must never sit behind browser-challenge security.

## D8 task portability decision (2026-07-16, CTO call, owner veto open)
- D8 residual RESOLVED: keep documented direction. Web/server tasks sync everywhere; desktop-local tasks stay OFF cloud sync by default; per-task local-vs-server prompt on desktop.
- Evidence (July 2026 research, sourced): Claude Code Remote Control is opt-in, off by default, live-steering not upload; teleport is one-way web to local; Codex cloud delegation is an explicit per-task push, transcripts local by default; Cowork Projects have no cloud sync. No vendor auto-uploads local sessions. Industry practice matches Hive direction exactly.
- Unblocks Step 4.4 (desktop firewall + portability, last Wave 4 demo step). Sources logged in vault log-2026-07-15-wave1-spike-execution.

## Stale-code purge + SDLC audit (2026-07-19, owner-directed)
- Deleted outright (git history = archive, owner choice): deploy/gcp, deploy/geo-router, deploy/oci, deploy/cloudflared, docker-compose.demo.yml, scripts/phase10-*, verify-requirements-matrix.sh, scripts/seed-demo, .planning/ (all of it; planning ground truth = Obsidian vault). PR #361.
- graphify-out fully untracked + gitignored (graph.json was 50MB per-session churn blob). Never re-track.
- Feature-gate category "carl" renamed to "agents" with idempotent supabase migration (PR #362); Carl/EnterpriseEdge names purged from issues, milestones, labels.
- OWNER DECISION: customer-USD lint + dedicated FX-guard test files REMOVED (PR #377). Tests = functionality/features only. Do NOT reintroduce fx-zero-leak guard-only tests or the lint. Runtime amount_usd omission behavior stays.
- Branch protection reconciled DOWNWARD to live 6 checks (owner choice, PR #363); Web E2E not required. MERGE-POLICY.md is the doc of record.
- Vault reorged: hive/README.md entry point, 11 live docs, 317 archived under hive/archive/ with ARCHIVE-SUMMARY.md.
- Adversarial audit issues #364-#376 (label audit-2026-07-19) = current process-debt backlog.

## Do-Not-Repeat additions (2026-07-19)
- NEVER wrap toolchain docker test commands in bash -c / sh -c (Alpine, ENTRYPOINT /bin/sh -c; double-wrap silently no-ops). Pass command string directly.
- CLAUDE.md edits are hook-gated to main agent via claude-md-management skill; do not brief subagents to edit CLAUDE.md.
