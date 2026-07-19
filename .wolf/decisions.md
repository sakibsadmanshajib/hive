# Decisions Ledger (lightweight index)

Terse locked-decision index. ONE or TWO lines each, pointing to the detail (Obsidian vault doc, issue, or in-repo ADR like VENDORING.md). Detail NEVER lives here, to keep .wolf light and out of the system prompt.

PROTOCOL:
- Read this before any spec, plan, design, or implementation. Inject the relevant decisions into every subagent brief (do not rely on agents rediscovering them).
- When the owner states a decision (a "decision:" moment), append a terse entry here immediately with a new D-ID and a pointer to where the detail lives.
- Supersede in place: mark the old entry SUPERSEDED by D-NNN, keep the line for history.

Format: `D-ID | decision (1 line) | source | date`

- D-001 | RAG embedding: admin selects the model AND its dimension; the system provisions the pgvector column/index to match and re-embeds existing docs; NO truncation band-aid | vault plan-admin-embedding-dim.md, issue #392 | 2026-07-19
- D-002 | Desktop sandbox = vendored OpenAI Codex Rust sandbox (bwrap + process-hardening + bubblewrap), NOT Tauri's default | apps/desktop-sandbox/VENDORING.md | 2026-07-16
- D-003 | Windows sandbox: ADOPT the elevated codex-windows-sandbox (dedicated sandbox OS user + UAC elevated runner + capability-restricted token + ACL + WFP egress scoped to sandbox SID + ConPTY), staged; the minimal restricted-token base variant is insufficient for the agentic + CLI flow. Step 1 (CreateProcessAsUserW launch fix) is a prerequisite under any option | vault plan-codex-crossplatform-desktop.md, issue #393, VENDORING.md | 2026-07-19
- D-004 | Owner reviews no PRs; agent owns the full loop (spec, impl, adversarial review, verify, merge); security boundaries merge only after real validation | memory feedback_agent_owns_full_pr_loop.md | 2026-07-19
- D-005 | No band-aid workarounds; solve the real design | .wolf/cerebrum.md Owner preferences | 2026-07-19
- D-006 | Gateway fronts all model types with per-model-type admin-controlled posture (self-host vs serverless), not a global switch | vault decision-2026-07-17-gateway-posture-and-network.md | 2026-07-17
- D-007 | One product, two modes: Hive (cloud SaaS) + Hive Enterprise (customer-hosted); single org = single tenant, departments via RBAC | vault project docs | 2026-07
- D-008 | Desktop must also provide a CLI environment (coding-agent CLI, Claude-Code-equivalent), following Codex's native cross-platform desktop model | owner 2026-07-19; research pending | 2026-07-19
- D-009 | Follow OpenAI Codex's native cross-platform (Windows/macOS/Linux) desktop + sandbox + CLI approach; research it and adopt or adapt rather than inventing a parallel design | owner 2026-07-19; vault plan-codex-crossplatform-desktop.md (pending research) | 2026-07-19

(Older historical decisions to be backfilled from the vault + code ADR-comments in a lean sweep.)
