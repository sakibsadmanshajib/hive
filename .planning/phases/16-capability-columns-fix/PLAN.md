---
phase: 16-capability-columns-fix
plan: 01
type: execute
wave: 1
depends_on: []
branch: a/phase-16-capability-columns-fix
milestone: v1.1
track: A
files_modified:
  - .planning/REQUIREMENTS.md
  - .planning/phases/16-capability-columns-fix/16-VERIFICATION.md
  - .planning/phases/16-capability-columns-fix/evidence/CAP-16-01.md
autonomous: true
requirements:
  - CAP-16-01
must_haves:
  truths:
    - "apps/control-plane/internal/routing/repository.go contains no function named ensureCapabilityColumns and no ALTER TABLE statement (DDL belongs in supabase/migrations/)."
    - "public.provider_capabilities (NOT route_capabilities) carries every capability column ListRouteCandidates selects: supports_responses, supports_chat_completions, supports_completions, supports_embeddings, supports_streaming, supports_reasoning, supports_cache_read, supports_cache_write, supports_image_generation, supports_image_edit, supports_tts, supports_stt, supports_batch."
    - "Existing guard test apps/control-plane/internal/routing/repository_schema_test.go::TestRoutingRepositoryDoesNotRunCapabilityDDL passes — proves the latent bug cannot reappear."
    - "ListRouteCandidates query in repository.go targets public.provider_capabilities via JOIN on route_id (not route_capabilities) and the existing TestListRouteCandidatesSelectsMediaColumns test passes."
    - "A CAP-16-01 evidence file exists in 16-capability-columns-fix/evidence/ with frontmatter (requirement_id, status, verified_at, verified_by, phase_satisfied, evidence) and links the guard test, the migration, and the repository.go source."
    - "16-VERIFICATION.md captures pass/fail for each must_have truth with the exact go-test command + output snippet, ready to feed v1.1.0 ship-gate audit."
    - ".planning/REQUIREMENTS.md gains a CAP-16-01 row pointing at the evidence file so scripts/verify-requirements-matrix.sh resolves the link."
  artifacts:
    - path: "apps/control-plane/internal/routing/repository.go"
      provides: "Routing repository with ListRouteCandidates joining provider_capabilities — no runtime DDL."
      contains: "FROM public.provider_routes r"
    - path: "apps/control-plane/internal/routing/repository_schema_test.go"
      provides: "Guard tests asserting no ensureCapabilityColumns / no ALTER TABLE / media columns selected."
      contains: "TestRoutingRepositoryDoesNotRunCapabilityDDL"
    - path: "supabase/migrations/20260414_01_provider_capabilities_media_columns.sql"
      provides: "Migration that adds media + batch columns to public.provider_capabilities (the correct table) and backfills route-openrouter-auto."
      contains: "alter table public.provider_capabilities"
    - path: ".planning/phases/16-capability-columns-fix/evidence/CAP-16-01.md"
      provides: "Per-requirement evidence file with frontmatter + integration-test command for CAP-16-01."
      contains: "requirement_id: CAP-16-01"
    - path: ".planning/phases/16-capability-columns-fix/16-VERIFICATION.md"
      provides: "Phase 16 verification log feeding v1.1.0 ship-gate."
      contains: "Must-Have Truth Verification"
    - path: ".planning/REQUIREMENTS.md"
      provides: "Active requirement matrix — gains CAP-16-01 row with Evidence link."
      contains: "CAP-16-01"
  key_links:
    - from: "apps/control-plane/internal/routing/repository_schema_test.go"
      to: "apps/control-plane/internal/routing/repository.go"
      via: "filesystem read + string assertion (no ensureCapabilityColumns / no ALTER TABLE)"
      pattern: "ensureCapabilityColumns"
    - from: "apps/control-plane/internal/routing/repository_schema_test.go"
      to: "supabase/migrations/20260414_01_provider_capabilities_media_columns.sql"
      via: "glob + ALTER TABLE assertion that targets provider_capabilities (NOT route_capabilities)"
      pattern: "alter table public.provider_capabilities"
    - from: ".planning/REQUIREMENTS.md"
      to: ".planning/phases/16-capability-columns-fix/evidence/CAP-16-01.md"
      via: "Evidence column markdown link"
      pattern: "evidence/CAP-16-01.md"
    - from: ".planning/phases/16-capability-columns-fix/16-VERIFICATION.md"
      to: "scripts/verify-requirements-matrix.sh"
      via: "logged invocation + captured exit code"
      pattern: "verify-requirements-matrix"
---

<objective>
Close the v1.1 Track-A bug ticket "ensureCapabilityColumns targets route_capabilities instead of provider_capabilities" by formally verifying that the latent bug is no longer present, the schema lives in migrations, and a regression-guard test prevents reintroduction.

Purpose: CLAUDE.md Known Issue #1 + V1.1-MASTER-PLAN.md §Phase 16 carry this as an unfixed latent bug. Discovery during planning (2026-04-25) shows that `apps/control-plane/internal/routing/repository.go` no longer contains `ensureCapabilityColumns` — Phase 14 media-columns work pulled the DDL out of the runtime path into `supabase/migrations/20260414_01_provider_capabilities_media_columns.sql` (which correctly targets `public.provider_capabilities`), and `apps/control-plane/internal/routing/repository_schema_test.go` already asserts the function and ALTER TABLE cannot reappear. Phase 16 therefore reduces to a verification-only phase: prove the bug is closed, write the evidence, update CLAUDE.md Known Issues + REQUIREMENTS.md, and ship-gate the ticket.

If discovery during execution reveals the bug is still live (e.g. function reintroduced on a side branch), the executor escalates via Blockers instead of fabricating Satisfied status — see Task 2.

Output:
- `.planning/phases/16-capability-columns-fix/evidence/CAP-16-01.md`
- `.planning/phases/16-capability-columns-fix/16-VERIFICATION.md`
- `.planning/REQUIREMENTS.md` (CAP-16-01 row added)
</objective>

<execution_context>
@/home/sakib/.claude/get-shit-done/workflows/execute-plan.md
@/home/sakib/.claude/get-shit-done/templates/summary.md
</execution_context>

<context>
@.planning/PROJECT.md
@.planning/ROADMAP.md
@.planning/STATE.md
@.planning/v1.1-DEFERRED-SCOPE.md
@.planning/v1.1-chatapp/V1.1-MASTER-PLAN.md
@.planning/REQUIREMENTS.md
@.planning/phases/11-verification-cleanup/PLAN.md
@apps/control-plane/internal/routing/repository.go
@apps/control-plane/internal/routing/repository_schema_test.go
@supabase/migrations/20260414_01_provider_capabilities_media_columns.sql

<interfaces>
<!-- Already-shipped artifacts that satisfy CAP-16-01. Executor must verify, NOT recreate. -->

apps/control-plane/internal/routing/repository.go:
- Has NO `ensureCapabilityColumns` function (grep returns zero hits).
- ListRouteCandidates joins public.provider_capabilities via `JOIN public.provider_capabilities c ON c.route_id = r.route_id`.
- No ALTER TABLE, no CREATE TABLE, no DDL at runtime.

apps/control-plane/internal/routing/repository_schema_test.go (regression guards — must keep passing):
- TestRoutingRepositoryDoesNotRunCapabilityDDL — asserts no `ensureCapabilityColumns` token and no `ALTER TABLE` token in repository.go source.
- TestProviderCapabilitiesMigrationAddsMediaColumns — asserts a migration exists altering `public.provider_capabilities` (rejects any migration that alters `route_capabilities`).
- TestProviderCapabilitiesMigrationBackfillsMediaAndBatchRoutes — asserts backfill for `route-openrouter-auto`.
- TestListRouteCandidatesSelectsMediaColumns — asserts `c.supports_image_generation`, `c.supports_image_edit`, `c.supports_tts`, `c.supports_stt`, `c.supports_batch` are selected.

supabase/migrations/20260414_01_provider_capabilities_media_columns.sql:
- `alter table public.provider_capabilities add column if not exists supports_image_generation/supports_image_edit/supports_tts/supports_stt/supports_batch`.
- Backfills `route-openrouter-auto` with all five columns set true.
- Targets the CORRECT table (provider_capabilities, NOT route_capabilities).

CLAUDE.md Known Issues §1 — wording must be retired by Phase 16 close.
</interfaces>
</context>

<tasks>

<task type="auto">
  <name>Task 1: Run guard tests + capture evidence for CAP-16-01</name>
  <files>
    .planning/phases/16-capability-columns-fix/evidence/CAP-16-01.md
  </files>
  <action>
    Step 1 — Re-confirm the bug is closed by running the existing regression-guard tests via the Docker toolchain (the project standard — host has no Go toolchain installed):

    ```
    cd deploy/docker && docker compose --profile tools run --rm toolchain bash -c \
      "cd /workspace && go test ./apps/control-plane/internal/routing/... -count=1 -short -run 'TestRoutingRepositoryDoesNotRunCapabilityDDL|TestProviderCapabilitiesMigrationAddsMediaColumns|TestProviderCapabilitiesMigrationBackfillsMediaAndBatchRoutes|TestListRouteCandidatesSelectsMediaColumns'"
    ```

    All four tests MUST pass. If any fail, STOP and escalate to Task 2 Blockers — do not proceed.

    Step 2 — Independently re-verify by static inspection (these checks duplicate the test contract, but provide a second signal for the verification log):

    ```
    ! grep -n "ensureCapabilityColumns" apps/control-plane/internal/routing/repository.go
    ! grep -niE "alter[[:space:]]+table" apps/control-plane/internal/routing/repository.go
    grep -n "JOIN public.provider_capabilities" apps/control-plane/internal/routing/repository.go
    grep -n "alter table public.provider_capabilities" supabase/migrations/20260414_01_provider_capabilities_media_columns.sql
    ! grep -niE "alter[[:space:]]+table[[:space:]]+(public\\.)?route_capabilities" supabase/migrations/*.sql
    ```

    The first two commands MUST produce no output (negation). Last command MUST produce no output (no migration alters route_capabilities). The middle two MUST produce at least one line each.

    Step 3 — Write `.planning/phases/16-capability-columns-fix/evidence/CAP-16-01.md` with frontmatter matching the Phase 11 evidence file convention (see `.planning/phases/11-verification-cleanup/evidence/AUTH-01.md` for shape). Required frontmatter keys: `requirement_id`, `status`, `verified_at`, `verified_by`, `phase_satisfied`, `evidence` (with `code_paths`, `integration_tests`, `summary_refs` sub-keys).

    Body must include:
    - `## Behavior` — one paragraph stating "ensureCapabilityColumns is not defined or called in repository.go; provider_capabilities schema lives in supabase migrations; ListRouteCandidates joins the correct table."
    - `## Code paths` — list `apps/control-plane/internal/routing/repository.go`, `apps/control-plane/internal/routing/repository_schema_test.go`, `supabase/migrations/20260414_01_provider_capabilities_media_columns.sql`.
    - `## Reproduce` — paste the exact `go test` command from Step 1.
    - `## Static checks` — paste the four grep commands from Step 2 with expected output annotated.
    - `## Known Caveats` — none (this is verification-only; bug is closed).

    Constraint: this task touches NO code under `apps/`, `packages/`, `supabase/`, `deploy/`. Only `.planning/` artifacts.
  </action>
  <verify>
    <automated>test -f .planning/phases/16-capability-columns-fix/evidence/CAP-16-01.md && grep -q "requirement_id: CAP-16-01" .planning/phases/16-capability-columns-fix/evidence/CAP-16-01.md && grep -q "phase_satisfied" .planning/phases/16-capability-columns-fix/evidence/CAP-16-01.md && grep -q "TestRoutingRepositoryDoesNotRunCapabilityDDL" .planning/phases/16-capability-columns-fix/evidence/CAP-16-01.md</automated>
  </verify>
  <done>
    Evidence file exists with valid YAML frontmatter (requirement_id=CAP-16-01, status=satisfied, verified_at, verified_by, phase_satisfied=16-capability-columns-fix, evidence block). Body lists the three code paths, the runnable go-test command, and the four static-check greps. No production source files modified.
  </done>
</task>

<task type="auto">
  <name>Task 2: Write 16-VERIFICATION.md ship-gate log + update REQUIREMENTS.md + retire Known Issue</name>
  <files>
    .planning/phases/16-capability-columns-fix/16-VERIFICATION.md,
    .planning/REQUIREMENTS.md,
    CLAUDE.md
  </files>
  <action>
    Step 1 — Produce `.planning/phases/16-capability-columns-fix/16-VERIFICATION.md` using the Phase 11 verification log shape (`.planning/phases/11-verification-cleanup/11-VERIFICATION.md` is the reference). Required sections:

    - Frontmatter: `phase: 16-capability-columns-fix`, `verified_at: 2026-04-25`, `verified_by`, `status` (set to `closed` only if every must_have truth passes; `blocked` otherwise).
    - `## Must-Have Truth Verification` — table with each must_have truth from this PLAN's frontmatter, the command run (the four-test go-test invocation + the four static-check greps), captured output snippet (head + tail), pass/fail.
    - `## Requirement Coverage` — single row for CAP-16-01 with evidence-file path, plus the validator exit code from `bash scripts/verify-requirements-matrix.sh` (which now resolves the new row).
    - `## Static-Check Evidence` — paste full output of the four grep commands from Task 1 Step 2.
    - `## Blockers` — explicit section. If during Task 1 Step 1 any of the four guard tests failed, list each failed test with output snippet here and set frontmatter `status: blocked`. Empty section is acceptable but the heading must exist.
    - `## v1.1.0 Ship-Gate Mapping` — explicit statement that this phase closes V1.1-MASTER-PLAN.md §Phase 16 and retires CLAUDE.md Known Issue #1 ("`ensureCapabilityColumns` targets wrong table"). Reference the v1.1.0 ship-gate item from §Cross-Phase Concerns.

    Step 2 — Update `.planning/REQUIREMENTS.md` (created in Phase 11). Add a new row under the v1.1 section:

    ```
    | CAP-16-01 | 16 | Satisfied | [evidence/CAP-16-01.md](phases/16-capability-columns-fix/evidence/CAP-16-01.md) |
    ```

    If the v1.1 section does not exist yet, create it under a `## v1.1 Requirements (in flight)` heading. Re-run `bash scripts/verify-requirements-matrix.sh` and capture exit code in 16-VERIFICATION.md.

    Step 3 — Edit `CLAUDE.md` `## Known Issues` section. Item #1 currently reads:

    > 1. **`ensureCapabilityColumns` targets wrong table** — `apps/control-plane/internal/routing/repository.go` targets `route_capabilities` instead of `provider_capabilities`. Latent bug — current routing works because separate seed path populates required columns. Fix tracked in v1.1.

    Replace the body (keep the numbered list shape) with a one-line "Resolved" entry pointing at Phase 16 evidence:

    > 1. **`ensureCapabilityColumns` targets wrong table** — Resolved by Phase 16 (2026-04-25). Function removed from `apps/control-plane/internal/routing/repository.go`; schema lives in `supabase/migrations/20260414_01_provider_capabilities_media_columns.sql` (correctly targets `public.provider_capabilities`); regression guard `TestRoutingRepositoryDoesNotRunCapabilityDDL` enforces non-recurrence. Evidence: `.planning/phases/16-capability-columns-fix/evidence/CAP-16-01.md`.

    Constraint: do NOT renumber items #2-#4 — they may need their own phase closure later. Preserve their existing wording.

    Step 4 — Run final verification end-to-end and capture in 16-VERIFICATION.md `## Final Sweep` subsection:
    ```
    bash scripts/verify-requirements-matrix.sh
    cd deploy/docker && docker compose --profile tools run --rm toolchain bash -c \
      "cd /workspace && go test ./apps/control-plane/internal/routing/... -count=1 -short"
    ```

    Both MUST exit 0. If either fails, set frontmatter `status: blocked` and list under Blockers.

    Constraint: Task 2 touches `.planning/`, `CLAUDE.md` only. Zero changes under `apps/`, `packages/`, `supabase/`, `deploy/`, `scripts/`.
  </action>
  <verify>
    <automated>test -f .planning/phases/16-capability-columns-fix/16-VERIFICATION.md && grep -q "Must-Have Truth Verification" .planning/phases/16-capability-columns-fix/16-VERIFICATION.md && grep -q "Ship-Gate" .planning/phases/16-capability-columns-fix/16-VERIFICATION.md && grep -q "Blockers" .planning/phases/16-capability-columns-fix/16-VERIFICATION.md && grep -q "CAP-16-01" .planning/REQUIREMENTS.md && grep -q "Resolved by Phase 16" CLAUDE.md && bash scripts/verify-requirements-matrix.sh</automated>
  </verify>
  <done>
    16-VERIFICATION.md has Must-Have Truth Verification table (all rows pass) + Requirement Coverage + Static-Check Evidence + Blockers + v1.1.0 Ship-Gate Mapping + Final Sweep sections. REQUIREMENTS.md has a CAP-16-01 row resolving to the evidence file (validator exits 0). CLAUDE.md Known Issues #1 is retired with a "Resolved by Phase 16" line linking to the evidence file. Items #2-#4 in Known Issues unchanged.
  </done>
</task>

</tasks>

<verification>
Phase-level verification commands (run after all tasks complete):

1. `cd deploy/docker && docker compose --profile tools run --rm toolchain bash -c "cd /workspace && go test ./apps/control-plane/internal/routing/... -count=1 -short"` — exits 0; all four guard tests pass.
2. `! grep -n "ensureCapabilityColumns" apps/control-plane/internal/routing/repository.go` — no output.
3. `! grep -niE "alter[[:space:]]+table[[:space:]]+(public\\.)?route_capabilities" supabase/migrations/*.sql` — no output.
4. `bash scripts/verify-requirements-matrix.sh` — exits 0; output mentions CAP-16-01.
5. `grep -q "Resolved by Phase 16" CLAUDE.md` — Known Issue #1 retired.
6. `git diff --name-only` reports zero changes under `apps/`, `packages/`, `supabase/`, `deploy/`, `scripts/`.

Expected outputs:
- (1) Go test summary `ok  github.com/hivegpt/hive/apps/control-plane/internal/routing`.
- (4) Validator prints `OK: N evidence files validated` where N >= 7 (Phase 11 baseline + CAP-16-01).
- (6) prints empty (no production code changes).
</verification>

<success_criteria>
Definition of Done — also serves as v1.1.0 ship-gate input for Phase 16:

- [ ] All four guard tests in `apps/control-plane/internal/routing/repository_schema_test.go` pass via Docker toolchain.
- [ ] `apps/control-plane/internal/routing/repository.go` contains no `ensureCapabilityColumns` and no `ALTER TABLE` (verified by grep + by guard test).
- [ ] No migration in `supabase/migrations/` alters `route_capabilities` (verified by grep).
- [ ] `.planning/phases/16-capability-columns-fix/evidence/CAP-16-01.md` exists with full frontmatter and body conforming to Phase 11 evidence-file convention.
- [ ] `.planning/REQUIREMENTS.md` carries a CAP-16-01 row whose Evidence link resolves on disk (`scripts/verify-requirements-matrix.sh` exits 0).
- [ ] `.planning/phases/16-capability-columns-fix/16-VERIFICATION.md` records pass/fail for every must_have truth, includes Static-Check Evidence + Blockers + v1.1.0 Ship-Gate Mapping + Final Sweep sections.
- [ ] `CLAUDE.md` Known Issue #1 retired with "Resolved by Phase 16" line linking to evidence — items #2-#4 unchanged.
- [ ] Zero production source files modified (`apps/`, `packages/`, `supabase/`, `deploy/`, `scripts/` clean per `git diff --name-only`).
- [ ] Branch `a/phase-16-capability-columns-fix` created and PR opened against main (single PR per V1.1-MASTER-PLAN.md branching strategy).

Ship-gate mapping: closes V1.1-MASTER-PLAN.md §Phase 16 and retires CLAUDE.md Known Issue #1. Contributes to the v1.1.0 ship-gate item "all v1.0 requirements have green verification artifacts" (from §Cross-Phase Concerns) by adding CAP-16-01 with evidence.
</success_criteria>

<blockers>
Discovered during planning (2026-04-25):

1. **The latent bug is already closed in worktree.** Grep across `apps/control-plane/` returns zero hits for `ensureCapabilityColumns` outside the regression-guard test that explicitly forbids it. Phase 14's media-columns work moved provider_capabilities DDL into `supabase/migrations/20260414_01_provider_capabilities_media_columns.sql` (which targets the correct table) and added `repository_schema_test.go` to prevent the function returning. **Phase 16 therefore reduces to verification-only** — no source-code edit, no new migration. CLAUDE.md Known Issue #1 wording is stale and the master plan §Phase 16 line ("Fix table reference in `apps/control-plane/internal/routing/repository.go`") is also stale relative to current main. Plan reflects this reality.

2. **No new migration needed.** The expected migration (`provider_capabilities` media + batch columns) already exists at `supabase/migrations/20260414_01_provider_capabilities_media_columns.sql` and is asserted by `TestProviderCapabilitiesMigrationAddsMediaColumns`. If executor finds an additional column required during verification (e.g. a column ListRouteCandidates selects but provider_capabilities lacks), they MUST stop and escalate via 16-VERIFICATION.md Blockers — do not author a new migration inside this phase without re-planning.

3. **Phase 11 dependency.** This plan adds a row to `.planning/REQUIREMENTS.md` and assumes `scripts/verify-requirements-matrix.sh` exists. Both are produced by Phase 11. If Phase 11 has not landed yet, executor MUST run Phase 11 first (or block) — Phase 16 cannot ship without the active matrix + validator. Master plan lists Phase 16 with `depends_on: []` because it is logically independent of Phase 11's content, but the verification-artifact wiring depends on Phase 11's infrastructure.

4. **Docker required.** The guard tests run only via `deploy/docker` toolchain (project rule — no host Go). If Docker is unavailable in the executor environment, the plan cannot complete; mark `status: blocked` in 16-VERIFICATION.md with the missing dependency.
</blockers>

<output>
After completion, create `.planning/phases/16-capability-columns-fix/16-01-SUMMARY.md` per the GSD summary template, recording:
- Files created (evidence/CAP-16-01.md, 16-VERIFICATION.md)
- Files modified (.planning/REQUIREMENTS.md, CLAUDE.md)
- Guard-test output (all four tests)
- Validator output (scripts/verify-requirements-matrix.sh exit code + summary line)
- Any Blockers carried forward
- Ship-gate status update for v1.1.0 (CAP-16-01 satisfied; Known Issue #1 retired)
</output>
