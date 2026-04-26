---
phase: 16-capability-columns-fix
plan: 01
verified_at: 2026-04-25
verified_by: gsd-execute-phase agent
status: closed
ship_gate: v1.1.0 — closes V1.1-MASTER-PLAN.md §Phase 16 and retires CLAUDE.md Known Issue #1
---

# Phase 16 Verification Log — `ensureCapabilityColumns` Wrong-Table Fix

Records pass/fail for every must-have truth in PLAN.md, captures the
regression-guard test output, the static-check grep evidence, and the
validator exit code. Phase 16 is verification-only — no production source
modified.

---

## Must-Have Truth Verification

| # | Truth (from PLAN.md) | Command | Output | Status |
|---|----------------------|---------|--------|--------|
| 1 | `repository.go` has no `ensureCapabilityColumns` and no runtime `ALTER TABLE` (DDL belongs in `supabase/migrations/`). | `grep -n 'ensureCapabilityColumns' apps/control-plane/internal/routing/repository.go ; grep -niE 'alter[[:space:]]+table' apps/control-plane/internal/routing/repository.go` | Both commands produce **no output** (exit 1). Source is clean. | PASS |
| 2 | `public.provider_capabilities` carries every capability column `ListRouteCandidates` selects (responses, chat_completions, completions, embeddings, streaming, reasoning, cache_read, cache_write, image_generation, image_edit, tts, stt, batch). | `TestListRouteCandidatesSelectsMediaColumns` (asserts `c.supports_image_generation`, `c.supports_image_edit`, `c.supports_tts`, `c.supports_stt`, `c.supports_batch` are selected); `TestProviderCapabilitiesMigrationAddsMediaColumns` (asserts the same five columns exist in the migration that alters `public.provider_capabilities`). | `--- PASS: TestListRouteCandidatesSelectsMediaColumns (0.00s)` and `--- PASS: TestProviderCapabilitiesMigrationAddsMediaColumns (0.00s)`. | PASS |
| 3 | Existing guard test `TestRoutingRepositoryDoesNotRunCapabilityDDL` passes — proves the latent bug cannot reappear. | `go test ./apps/control-plane/internal/routing/... -run TestRoutingRepositoryDoesNotRunCapabilityDDL -v` | `--- PASS: TestRoutingRepositoryDoesNotRunCapabilityDDL (0.00s)`. | PASS |
| 4 | `ListRouteCandidates` query targets `public.provider_capabilities` via JOIN on `route_id` (not `route_capabilities`); `TestListRouteCandidatesSelectsMediaColumns` passes. | `grep -n 'JOIN public.provider_capabilities' apps/control-plane/internal/routing/repository.go` + the test invocation above. | `86:    JOIN public.provider_capabilities c ON c.route_id = r.route_id` and the test passes. | PASS |
| 5 | A CAP-16-01 evidence file exists with frontmatter (`requirement_id`, `status`, `verified_at`, `verified_by`, `phase_satisfied`, `evidence`) and links the guard test, the migration, and `repository.go`. | `test -f .planning/phases/16-capability-columns-fix/evidence/CAP-16-01.md && grep -E '^(requirement_id\|status\|verified_at\|verified_by\|phase_satisfied\|evidence):' .planning/phases/16-capability-columns-fix/evidence/CAP-16-01.md` | File present; all six frontmatter keys present; body lists three code paths + runnable go-test command + five static checks. | PASS |
| 6 | This 16-VERIFICATION.md captures pass/fail for each must-have truth with exact go-test command + output snippet, ready for v1.1.0 ship-gate audit. | `test -f .planning/phases/16-capability-columns-fix/16-VERIFICATION.md` | This file. | PASS |
| 7 | `.planning/REQUIREMENTS.md` gains a CAP-16-01 row pointing at the evidence file so `scripts/verify-requirements-matrix.sh` resolves the link. | `grep -n 'CAP-16-01' .planning/REQUIREMENTS.md ; bash scripts/verify-requirements-matrix.sh` | Row present under v1.1 Routing & Catalog section; validator prints `OK: 7 evidence files validated` and exits 0. | PASS |

---

## Requirement Coverage

| Requirement | Evidence file | Validator result |
|-------------|---------------|------------------|
| CAP-16-01 | `.planning/phases/16-capability-columns-fix/evidence/CAP-16-01.md` | OK (frontmatter complete; file resolvable) |

**Validator command + output:**

```
$ bash scripts/verify-requirements-matrix.sh
OK: 7 evidence files validated
$ echo $?
0
```

The matrix now resolves seven evidence files: AUTH-01, AUTH-02, API-01,
API-02, API-03, API-04 (Phase 11 baseline), plus the new CAP-16-01.

---

## Static-Check Evidence

All five grep assertions from PLAN.md Task 1 Step 2 captured below,
executed against worktree HEAD on `a/phase-16-capability-columns-fix`:

```
$ grep -n 'ensureCapabilityColumns' apps/control-plane/internal/routing/repository.go
(no output — exit 1)

$ grep -niE 'alter[[:space:]]+table' apps/control-plane/internal/routing/repository.go
(no output — exit 1)

$ grep -n 'JOIN public.provider_capabilities' apps/control-plane/internal/routing/repository.go
86:		JOIN public.provider_capabilities c ON c.route_id = r.route_id

$ grep -n 'alter table public.provider_capabilities' supabase/migrations/20260414_01_provider_capabilities_media_columns.sql
1:alter table public.provider_capabilities

$ grep -niE 'alter[[:space:]]+table[[:space:]]+(public\.)?route_capabilities' supabase/migrations/*.sql
(no output — exit 1)
```

The first, second, and fifth checks are negation assertions and produce no
output. The third and fourth produce a single line each. All five evidence
the bug is closed at the source level.

---

## Guard Test Evidence

Full output from the four-test invocation via Docker toolchain
(`hive-toolchain:latest` image):

```
$ docker run --rm --entrypoint sh -v $PWD:/workspace -w /workspace hive-toolchain:latest \
    -c "go test ./apps/control-plane/internal/routing/... -count=1 -short \
        -run 'TestRoutingRepositoryDoesNotRunCapabilityDDL|TestProviderCapabilitiesMigrationAddsMediaColumns|TestProviderCapabilitiesMigrationBackfillsMediaAndBatchRoutes|TestListRouteCandidatesSelectsMediaColumns' -v"

=== RUN   TestRoutingRepositoryDoesNotRunCapabilityDDL
--- PASS: TestRoutingRepositoryDoesNotRunCapabilityDDL (0.00s)
=== RUN   TestProviderCapabilitiesMigrationAddsMediaColumns
--- PASS: TestProviderCapabilitiesMigrationAddsMediaColumns (0.00s)
=== RUN   TestProviderCapabilitiesMigrationBackfillsMediaAndBatchRoutes
--- PASS: TestProviderCapabilitiesMigrationBackfillsMediaAndBatchRoutes (0.00s)
=== RUN   TestListRouteCandidatesSelectsMediaColumns
--- PASS: TestListRouteCandidatesSelectsMediaColumns (0.00s)
PASS
ok  	github.com/hivegpt/hive/apps/control-plane/internal/routing	0.005s
```

Note on `compose run` invocation: `deploy/docker/docker-compose.yml` defines
the toolchain under the `tools` profile but the `control-plane` service
declares `depends_on: [redis]`, which lives under the `local` profile. This
worktree therefore invokes the toolchain via direct `docker run` against the
prebuilt `hive-toolchain:latest` image (mounting the worktree at
`/workspace`) — semantically identical to the documented compose form
`docker compose --profile tools --profile local run --rm toolchain sh -c
"cd /workspace && go test ..."`.

---

## Final Sweep

Full routing-package suite + validator after evidence was committed:

```
$ docker run --rm --entrypoint sh -v $PWD:/workspace -w /workspace hive-toolchain:latest \
    -c "go test ./apps/control-plane/internal/routing/... -count=1 -short"
ok  	github.com/hivegpt/hive/apps/control-plane/internal/routing	0.006s

$ bash scripts/verify-requirements-matrix.sh
OK: 7 evidence files validated
$ echo $?
0
```

Both exit 0. No regressions introduced.

---

## Diff Surface (no production source touched)

```
$ git diff --name-only main..HEAD | grep -E '^(apps|packages|supabase|deploy|scripts)/' | wc -l
0
```

Phase 16 modifies only:

- `.planning/phases/16-capability-columns-fix/PLAN.md` (created)
- `.planning/phases/16-capability-columns-fix/evidence/CAP-16-01.md` (created)
- `.planning/phases/16-capability-columns-fix/16-VERIFICATION.md` (this file, created)
- `.planning/phases/16-capability-columns-fix/16-01-SUMMARY.md` (created — see GSD summary)
- `.planning/REQUIREMENTS.md` (one row appended under new v1.1 Routing & Catalog section)
- `CLAUDE.md` (Known Issues §1 retired in place; items §2–§4 unchanged)

---

## Blockers

None.

The Phase 16 latent bug
(`ensureCapabilityColumns` issuing DDL against `route_capabilities`) was
already resolved by Phase 14's media-columns work on 2026-04-14 — the DDL
was lifted out of the runtime path into
`supabase/migrations/20260414_01_provider_capabilities_media_columns.sql`,
which targets `public.provider_capabilities`, and
`repository_schema_test.go` was added to prevent reintroduction. Phase 16
formally captures that closure as evidence and retires the stale Known
Issue line in `CLAUDE.md`.

---

## v1.1.0 Ship-Gate Mapping

Per `.planning/v1.1-chatapp/V1.1-MASTER-PLAN.md` §Cross-Phase Concerns, the
v1.1.0 ship-gate requires **"all v1.0 requirements have green verification
artifacts."**

Phase 16 contributes to that ship-gate by:

- Adding **CAP-16-01** to the active requirements matrix with a Satisfied
  status and a resolvable evidence link — counted by
  `scripts/verify-requirements-matrix.sh`.
- Retiring `CLAUDE.md` Known Issues §1 ("`ensureCapabilityColumns` targets
  wrong table") with an explicit "Resolved by Phase 16" line linking the
  evidence file. The known-issue list now reflects only items §2–§4 which
  are tracked by their own phases.
- Closing `V1.1-MASTER-PLAN.md` §Phase 16 — the Track-A ticket for this
  latent bug is satisfied with no production-code edit required, because
  Phase 14 had already eliminated the function.

Status: **closed**.
