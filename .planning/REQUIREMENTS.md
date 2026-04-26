# Hive Requirement Matrix (active)

**Created:** 2026-04-25 (Phase 11).
**Supersedes:** archived `.planning/milestones/v1.0-REQUIREMENTS.md` for **live** status.
The archive remains the v1.0 ship-gate snapshot (frozen 2026-04-21).

This file is the active source of truth for v1.0 + v1.1 requirement status. Each
row's `Evidence` column either resolves to an on-disk evidence file under
`.planning/phases/.../evidence/` (Satisfied / Partial) or names the planned
phase target (Pending). The validator
`scripts/verify-requirements-matrix.sh` enforces that every link of the first
form points at an existing file with required frontmatter.

---

## v1.0 Requirements (shipped 2026-04-21)

### Compatibility & Contract

| ID | Phase | Status | Evidence |
|----|-------|--------|----------|
| COMP-01 | 01 | Satisfied | Phase 01 (archive — pre-Phase-11 evidence in `milestones/v1.0-REQUIREMENTS.md`) |
| COMP-02 | 01 | Satisfied | Phase 01 (archive — pre-Phase-11 evidence in `milestones/v1.0-REQUIREMENTS.md`) |
| COMP-03 | 01 | Satisfied | Phase 01 (archive — pre-Phase-11 evidence in `milestones/v1.0-REQUIREMENTS.md`) |

### Inference Surface

| ID | Phase | Status | Evidence |
|----|-------|--------|----------|
| API-01 | 06 | Satisfied | [evidence/API-01.md](phases/11-verification-cleanup/evidence/API-01.md) |
| API-02 | 06 | Satisfied | [evidence/API-02.md](phases/11-verification-cleanup/evidence/API-02.md) |
| API-03 | 06 | Satisfied | [evidence/API-03.md](phases/11-verification-cleanup/evidence/API-03.md) |
| API-04 | 06 | Satisfied | [evidence/API-04.md](phases/11-verification-cleanup/evidence/API-04.md) |
| API-05 | 10 | Satisfied | Phase 10 (archive — `phases/10-routing-storage-critical-fixes/10-UAT.md` Test 7) |
| API-06 | 10 | Satisfied | Phase 10 (archive — `phases/10-routing-storage-critical-fixes/10-UAT.md` Test 8) |
| API-07 | 10 | Partial | Phase 10 (archive — `phases/10-routing-storage-critical-fixes/KNOWN-ISSUE-batch-upstream.md`); success-path Phase 12 (planned) |
| API-08 | 01 | Satisfied | Phase 01 (archive) |

### Model Catalog & Routing

| ID | Phase | Status | Evidence |
|----|-------|--------|----------|
| ROUT-01 | 04 | Satisfied | Phase 04 (archive) |
| ROUT-02 | 10 | Satisfied | Phase 10 (archive — `phases/10-routing-storage-critical-fixes/10-VERIFICATION.md`) |
| ROUT-03 | 04 | Satisfied | Phase 04 (archive) |

### API Keys & Attribution (v1.0 subset)

| ID | Phase | Status | Evidence |
|----|-------|--------|----------|
| KEY-04 | 10 | Partial | Phase 10 (archive — edge-level reservation attribution verified; success-path attribution unexercisable until API-07 success-path lands) |

### Authentication & Accounts (Phase 02 — recovered v1.0 satisfied)

The archived v1.0 matrix listed AUTH-01 / AUTH-02 as "Pending — Deferred v1.1".
Audit on 2026-04-25 (Phase 11 Task 1) confirmed Phase 02 shipped the underlying
code paths (Supabase auth migrations, web-console `/auth/{sign-up,sign-in,forgot-password,reset-password,callback}`
routes, `middleware.ts` session gate, control-plane account/membership
provisioning). Status corrected to **Satisfied** with evidence files below.
AUTH-03 + AUTH-04 remain Pending and route to a future phase — out of scope for
Phase 11.

| ID | Phase | Status | Evidence |
|----|-------|--------|----------|
| AUTH-01 | 02 | Satisfied | [evidence/AUTH-01.md](phases/11-verification-cleanup/evidence/AUTH-01.md) |
| AUTH-02 | 02 | Satisfied | [evidence/AUTH-02.md](phases/11-verification-cleanup/evidence/AUTH-02.md) |
| AUTH-03 | TBD | Pending | Phase TBD (planned) |
| AUTH-04 | TBD | Pending | Phase TBD (planned) |

---

## v1.1 Requirements — Deferred from v1.0

These were scoped to v1.0 originally but reassigned to v1.1 phases. Status
remains **Pending** until the target phase produces an evidence file.

### Billing & Payments

| ID | Phase | Status | Evidence |
|----|-------|--------|----------|
| BILL-01 | 11 | Pending | Phase 11 (planned — formal verification artifact deferred to ship-gate audit) |
| BILL-02 | 11 | Pending | Phase 11 (planned — formal verification artifact deferred to ship-gate audit) |
| BILL-03 | 13 | Pending | Phase 13 (planned) |
| BILL-04 | 11 | Pending | Phase 11 (planned — math shipped Phase 08; formal artifact deferred) |
| BILL-05 | 14 | Pending | Phase 14 (planned) |
| BILL-06 | 14 | Pending | Phase 14 (planned) |
| BILL-07 | 13 | Pending | Phase 13 (planned) |

### API Keys & Rate Limits

| ID | Phase | Status | Evidence |
|----|-------|--------|----------|
| KEY-01 | 13 | Pending | Phase 13 (planned) |
| KEY-02 | 12 | Pending | Phase 12 (planned) |
| KEY-03 | 13 | Pending | Phase 13 (planned) |
| KEY-05 | 12 | Pending | Phase 12 (planned) |

### Developer Console

| ID | Phase | Status | Evidence |
|----|-------|--------|----------|
| CONS-01 | 13 | Pending | Phase 13 (planned) |
| CONS-02 | 13 | Pending | Phase 13 (planned) |
| CONS-03 | 11 | Pending | Phase 11 (planned — chart UIs shipped Phase 09; live-data verification deferred) |

### Privacy & Operations

| ID | Phase | Status | Evidence |
|----|-------|--------|----------|
| PRIV-01 | 11 | Pending | Phase 11 (planned — policy enforced in code; formal VERIFICATION.md deferred) |
| OPS-01 | 11 | Pending | Phase 11 (planned — Prometheus/Grafana/Alertmanager shipped Phase 09; live-stack verification deferred) |

---

## Out of Scope

| Feature | Reason |
|---------|--------|
| End-user chat web application | Launch is strictly a developer API + control-plane product. |
| RAG projects or workspaces | Requires separate retrieval, workspace, content-governance semantics. |
| Hosted code runner or dev environment | Separate isolation + cost model from the API launch. |
| Credit subscriptions at launch | Commercial model is prepaid only for v1. |
| Customer-supplied upstream provider keys | Hive manages provider credentials internally; provider identity hidden. |
| OpenAI org/admin management endpoints | Not part of the drop-in developer value proposition. |
| Storing prompt or completion bodies by default | Conflicts with launch privacy requirement. |

---

## v2 Requirements (Out of v1.0 + v1.1)

- **SDK-01**: First-party branded SDK wrappers for JS/TS, Python, Java.
- **SUBS-01**: Subscription-like credit bundles resolving to Hive Credits.
- **ENT-01**: Org hierarchies, procurement controls, approval workflows.
- **ANAL-01**: Warehouse-backed deep analytics.

---

## Validator

`scripts/verify-requirements-matrix.sh` parses this file, extracts every
`[label](phases/.../evidence/*.md)` link, asserts the file exists with required
frontmatter (`requirement_id`, `status`, `verified_at`, `verified_by`,
`evidence`), and exits non-zero on any miss. Rows whose Evidence column reads
`Phase NN (planned)` or `Phase NN (archive ...)` are skipped — the former is an
intentional pending marker, the latter points back at archived v1.0 evidence
predating Phase 11.

---

*Active matrix established 2026-04-25 by Phase 11 — Compliance, Verification &
Artifact Cleanup. Archive: `.planning/milestones/v1.0-REQUIREMENTS.md`.*
