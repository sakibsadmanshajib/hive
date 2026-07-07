# Hive North Star Goal (owner-set, orchestrator-executed)

## Mission
Be the AI platform of Bangladesh: chat workstation plus OpenAI-compatible developer API, prepaid BDT, one product in two SKUs (Hive Cloud hosted, EnterpriseEdge self-hosted one-liner), built to investor-demo quality on <$50/month.

## Current goal: MVP DEMO-READY by 2026-06-20

Definition of done, every box checkable by a command or URL:

- [x] Staging live: api-hive.scubed.co and cp-hive.scubed.co return 200 behind Cloudflare
- [x] Full deploy pipeline green end to end (Go images, VM, Workers console, SDK replay)
- [x] One-line installer on main (curl raw install.sh, hardware advisor included)
- [x] Tool-call routing merged (#206): SDK tools requests succeed on capable aliases
- [x] Tool-call live proof: chat completion with tools returns tool_calls on staging (verified 2026-06-12, finish_reason tool_calls via SDK replay log)
- [ ] Phase 20 fully merged (#204, #205 remaining) plus wave 4 VERIFICATION.md
- [x] hive.scubed.co marketing site live (verified externally via r.jina.ai server-side fetch: 200, title Hive Bangladesh AI platform. Owner-local browser needs DNS cache flush, stale negative TTL)
- [ ] Chat workstation happy path on staging: signup, chat, file RAG, history (Phase 25 re-audit)
- [ ] bn-BD translation submitted upstream (owner 10-min action) and tracked
- [x] Demo script written: 10-minute investor walkthrough touching chat, API, installer, billing (.planning/demo/INVESTOR-DEMO.md, PR #210 merged)
- [ ] Budget intact: cost ledger shows <$50/month run rate

## Next goal after: v1.1 SHIPPED by 2026-06-30 (board target), then v1.2 agentic surface per roadmap.

## Operating commitments (how the orchestrator does not get stuck)
1. fleet.json updated on EVERY dispatch, completion, AND failure (state: failed, reason, relaunched-as). No silent deaths.
2. Every turn opens with a ground-truth sweep when any agent is in flight; stalled >20 min in a wait state = presumed dead, relaunch.
3. Goal checkboxes updated as they flip; owner can ask "goal status" anytime and get this file's truth.
4. INCIDENT 2026-06-12: .wolf/ wiped by agent git clean on shared checkout. Rule: every subagent brief forbids git clean; .wolf/ backed up to ~/.hive-wolf-backup.tar after every memory write.
