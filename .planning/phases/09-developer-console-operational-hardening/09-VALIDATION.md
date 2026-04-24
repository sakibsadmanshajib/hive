---
phase: 09
slug: developer-console-operational-hardening
status: draft
nyquist_compliant: false
wave_0_complete: false
created: 2026-04-10
---

# Phase 09 — Validation Strategy

> Per-phase validation contract for feedback sampling during execution.

---

## Test Infrastructure

| Property | Value |
|----------|-------|
| **Framework** | go test (control-plane) / vitest (console) |
| **Config file** | `console/vitest.config.ts` / Go test conventions |
| **Quick run command** | `cd console && npx vitest run --reporter=verbose` / `cd control-plane && go test ./...` |
| **Full suite command** | `cd console && npx vitest run && cd ../control-plane && go test ./...` |
| **Estimated runtime** | ~30 seconds |

---

## Sampling Rate

- **After every task commit:** Run quick run command for affected module
- **After every plan wave:** Run full suite command
- **Before `/gsd:verify-work`:** Full suite must be green
- **Max feedback latency:** 30 seconds

---

## Per-Task Verification Map

| Task ID | Plan | Wave | Requirement | Test Type | Automated Command | File Exists | Status |
|---------|------|------|-------------|-----------|-------------------|-------------|--------|
| 09-01-01 | 01 | 1 | CONS-01 | unit | `cd console && npx vitest run` | ❌ W0 | ⬜ pending |
| 09-01-02 | 01 | 1 | BILL-05 | unit | `cd console && npx vitest run` | ❌ W0 | ⬜ pending |
| 09-01-03 | 01 | 1 | CONS-02 | unit | `cd console && npx vitest run` | ❌ W0 | ⬜ pending |
| 09-02-01 | 02 | 2 | CONS-03 | unit | `cd console && npx vitest run` | ❌ W0 | ⬜ pending |
| 09-02-02 | 02 | 2 | BILL-06 | unit+integration | `cd console && npx vitest run` | ❌ W0 | ⬜ pending |
| 09-02-03 | 02 | 2 | CONS-03 | unit | `cd console && npx vitest run` | ❌ W0 | ⬜ pending |
| 09-03-01 | 03 | 3 | OPS-01 | integration | `cd control-plane && go test ./...` | ❌ W0 | ⬜ pending |
| 09-03-02 | 03 | 3 | OPS-01 | integration | `cd control-plane && go test ./...` | ❌ W0 | ⬜ pending |

*Status: ⬜ pending · ✅ green · ❌ red · ⚠️ flaky*

---

## Wave 0 Requirements

- [ ] `console/vitest.config.ts` — vitest configuration if not present
- [ ] `console/src/__tests__/` — test directory structure for console components
- [ ] `control-plane/internal/monitoring/` — test stubs for metrics/alerting

*If none: "Existing infrastructure covers all phase requirements."*

---

## Manual-Only Verifications

| Behavior | Requirement | Why Manual | Test Instructions |
|----------|-------------|------------|-------------------|
| BDT checkout suppresses FX language | BILL-05 | Visual verification of hidden elements | Load checkout with `accountCountryCode=BD`, verify no FX/USD text rendered |
| Grafana dashboard renders metrics | OPS-01 | Requires running Prometheus + Grafana stack | Start monitoring profile, verify dashboards load with sample data |

---

## Validation Sign-Off

- [ ] All tasks have `<automated>` verify or Wave 0 dependencies
- [ ] Sampling continuity: no 3 consecutive tasks without automated verify
- [ ] Wave 0 covers all MISSING references
- [ ] No watch-mode flags
- [ ] Feedback latency < 30s
- [ ] `nyquist_compliant: true` set in frontmatter

**Approval:** pending
