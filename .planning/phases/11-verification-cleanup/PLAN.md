---
phase: 11-verification-cleanup
plan: 01
type: execute
wave: 1
depends_on: []
branch: a/phase-11-verification-cleanup
milestone: v1.1
track: A
files_modified:
  - .planning/REQUIREMENTS.md
  - .planning/UAT-REPORT.md
  - .planning/phases/11-verification-cleanup/11-VERIFICATION.md
  - .planning/phases/11-verification-cleanup/evidence/AUTH-01.md
  - .planning/phases/11-verification-cleanup/evidence/AUTH-02.md
  - .planning/phases/11-verification-cleanup/evidence/API-01.md
  - .planning/phases/11-verification-cleanup/evidence/API-02.md
  - .planning/phases/11-verification-cleanup/evidence/API-03.md
  - .planning/phases/11-verification-cleanup/evidence/API-04.md
  - scripts/verify-requirements-matrix.sh
autonomous: true
requirements:
  - AUTH-01
  - AUTH-02
  - API-01
  - API-02
  - API-03
  - API-04
must_haves:
  truths:
    - "An active .planning/REQUIREMENTS.md exists at repo root and lists every v1.0 requirement with phase, status, and evidence-link columns."
    - "AUTH-01 and AUTH-02 each reference a concrete code path (control-plane + web-console + supabase migration) that satisfies them, plus an evidence file in 11-verification-cleanup/evidence/."
    - "API-01..API-04 each have a verification artifact with required frontmatter (requirement_id, verified_at, verified_by, evidence) and at least one runnable integration-test command linking to existing Phase 06 SDK harness output."
    - "UAT-REPORT.md either drops or annotates entries that are no longer accurate after Phase 10 closure (storage status, batch upstream blocker)."
    - "A scripts/verify-requirements-matrix.sh validator parses REQUIREMENTS.md, asserts every requirement has an evidence link reachable on disk, and exits non-zero on any miss."
    - "11-VERIFICATION.md captures pass/fail for each must_have with command + output snippet, ready to feed v1.1.0 ship-gate audit."
  artifacts:
    - path: ".planning/REQUIREMENTS.md"
      provides: "Active requirement matrix for v1.0 + v1.1 (currently archived only at .planning/milestones/v1.0-REQUIREMENTS.md)."
      contains: "AUTH-01"
    - path: ".planning/phases/11-verification-cleanup/evidence/"
      provides: "Per-requirement evidence files with frontmatter + integration-test commands."
      contains: "requirement_id"
    - path: "scripts/verify-requirements-matrix.sh"
      provides: "CI-callable validator — exits 0 only when every requirement row has a resolvable evidence path."
      contains: "REQUIREMENTS.md"
    - path: ".planning/phases/11-verification-cleanup/11-VERIFICATION.md"
      provides: "Phase 11 verification log feeding v1.1.0 ship-gate."
      contains: "AUTH-01"
  key_links:
    - from: ".planning/REQUIREMENTS.md"
      to: ".planning/phases/11-verification-cleanup/evidence/AUTH-01.md"
      via: "evidence column markdown link per row"
      pattern: "evidence/AUTH-01.md"
    - from: "scripts/verify-requirements-matrix.sh"
      to: ".planning/REQUIREMENTS.md"
      via: "grep + path-existence loop"
      pattern: "REQUIREMENTS.md"
    - from: ".planning/phases/11-verification-cleanup/11-VERIFICATION.md"
      to: "scripts/verify-requirements-matrix.sh"
      via: "logged invocation + captured exit code"
      pattern: "verify-requirements-matrix"
---

<objective>
Close v1.0 traceability gaps so the v1.1.0 tag is unblocked. Phase 11 is metadata + audit only — no production code changes. Re-establish an active `.planning/REQUIREMENTS.md` (currently only archived under `.planning/milestones/`), wire AUTH-01/AUTH-02 to real code paths, give API-01..API-04 verification artifacts with frontmatter + integration evidence, audit `UAT-REPORT.md` for stale entries after Phase 10 closure, and add a CI-callable validator that proves every requirement row resolves to an on-disk evidence file.

Purpose: v1.1.0 ship-gate (per V1.1-MASTER-PLAN.md §Cross-Phase Concerns) requires "all v1.0 requirements have green verification artifacts." Today the active REQUIREMENTS.md does not exist, AUTH-01/AUTH-02 still say "Pending — Deferred v1.1" in the archived matrix, and Phase 06 SDK harness output is not linked from any requirement row. This phase produces the audit + the guardrail.

Output:
- `.planning/REQUIREMENTS.md` (recreated/refreshed)
- `.planning/phases/11-verification-cleanup/evidence/{AUTH-01,AUTH-02,API-01,API-02,API-03,API-04}.md`
- `scripts/verify-requirements-matrix.sh`
- `.planning/UAT-REPORT.md` (annotated for Phase-10 reality)
- `.planning/phases/11-verification-cleanup/11-VERIFICATION.md`
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
@.planning/milestones/v1.0-REQUIREMENTS.md
@.planning/milestones/v1.0-MILESTONE-AUDIT.md
@.planning/UAT-REPORT.md
@.planning/phases/02-identity-account-foundation
@.planning/phases/06-core-text-embeddings-api
@.planning/phases/10-routing-storage-critical-fixes/10-UAT.md

<interfaces>
<!-- Existing requirement IDs from .planning/milestones/v1.0-REQUIREMENTS.md that this phase wires evidence for. -->

API-01: chat/completions, completions, responses — OpenAI-compatible request/response. Satisfied Phase 06.
API-02: SSE streaming + terminal events. Satisfied Phase 06.
API-03: embeddings — OpenAI-compatible. Satisfied Phase 06.
API-04: reasoning/thinking parameters + translated reasoning outputs. Satisfied Phase 06.
AUTH-01: signup/signin via Supabase. Archived as "Pending — Deferred v1.1".
AUTH-02: email verification + password reset. Archived as "Pending — Deferred v1.1".

<!-- Real code paths that already satisfy AUTH-01/02 (Phase 02 shipped these). Evidence files must point at these. -->
- supabase/migrations/* (auth schema bootstrap)
- apps/control-plane/internal/accounts/* (account/profile/membership)
- apps/web-console/app/auth/** (signin, signup, callback, reset-password)
- apps/web-console/middleware.ts (session gate)

<!-- Phase 06 SDK harness — already in repo, source of truth for API-01..04 integration evidence. -->
- packages/sdk-tests/* (JS/Python/Java SDK integration tests)
- deploy/docker docker-compose.yml profile=test (runs sdk-tests)
</interfaces>
</context>

<tasks>

<task type="auto">
  <name>Task 1: Audit + recreate active REQUIREMENTS.md and write evidence files</name>
  <files>
    .planning/REQUIREMENTS.md,
    .planning/phases/11-verification-cleanup/evidence/AUTH-01.md,
    .planning/phases/11-verification-cleanup/evidence/AUTH-02.md,
    .planning/phases/11-verification-cleanup/evidence/API-01.md,
    .planning/phases/11-verification-cleanup/evidence/API-02.md,
    .planning/phases/11-verification-cleanup/evidence/API-03.md,
    .planning/phases/11-verification-cleanup/evidence/API-04.md
  </files>
  <action>
    Step 1 — Audit. Read `.planning/milestones/v1.0-REQUIREMENTS.md` and produce an internal gap list of every requirement ID with its current phase, status, and whether an evidence file exists. Treat AUTH-01..AUTH-04 as the priority gap (archived shows "Pending — Deferred v1.1" but Phase 02 SUMMARY in `.planning/phases/02-identity-account-foundation/` already shipped Supabase signup, signin, email verify, password reset, and middleware session gating — confirm by skim of SUMMARY files there).

    Step 2 — Recreate `.planning/REQUIREMENTS.md` at repo root. Use the same column shape as the archived v1.0 file but add an `Evidence` column. Structure:

    ```
    # Hive Requirement Matrix (active)

    Active matrix — supersedes archived `.planning/milestones/v1.0-REQUIREMENTS.md` for live status.
    Archive remains the v1.0 ship-gate snapshot.

    ## v1.0 Requirements (shipped 2026-04-21)

    | ID | Phase | Status | Evidence |
    |----|-------|--------|----------|
    | API-01 | 06 | Satisfied | [evidence/API-01.md](phases/11-verification-cleanup/evidence/API-01.md) |
    | API-02 | 06 | Satisfied | [evidence/API-02.md](phases/11-verification-cleanup/evidence/API-02.md) |
    | API-03 | 06 | Satisfied | [evidence/API-03.md](phases/11-verification-cleanup/evidence/API-03.md) |
    | API-04 | 06 | Satisfied | [evidence/API-04.md](phases/11-verification-cleanup/evidence/API-04.md) |
    | AUTH-01 | 02 | Satisfied | [evidence/AUTH-01.md](phases/11-verification-cleanup/evidence/AUTH-01.md) |
    | AUTH-02 | 02 | Satisfied | [evidence/AUTH-02.md](phases/11-verification-cleanup/evidence/AUTH-02.md) |
    ... (carry forward every row from archived v1.0 matrix; mark requirements still pending as Pending and link to v1.1 phase target rather than to a non-existent evidence file)
    ```

    For requirements that have no evidence file in this phase yet (AUTH-03, AUTH-04, KEY-05, etc), set Evidence column to the planned phase id (e.g. `Phase 12 (planned)`) so the validator does not file-resolve them but ship-gate audit can still see the link target.

    Step 3 — Write each evidence file with required frontmatter:

    ```
    ---
    requirement_id: AUTH-01
    status: satisfied
    verified_at: 2026-04-25
    verified_by: gsd-plan-phase agent
    phase_satisfied: 02-identity-account-foundation
    evidence:
      code_paths:
        - apps/control-plane/internal/accounts/
        - apps/web-console/app/auth/
        - supabase/migrations/
      integration_tests:
        - cd deploy/docker && docker compose --env-file ../../.env --profile test up --build sdk-tests-js
      summary_refs:
        - .planning/phases/02-identity-account-foundation/02-01-SUMMARY.md
        - .planning/phases/02-identity-account-foundation/02-03-SUMMARY.md
    ---

    # AUTH-01 Evidence — signup/signin via Supabase

    ## Behavior
    User can sign up + sign in using Supabase auth via web-console; control-plane recognizes the session via middleware; an account row + default membership are provisioned on first signin.

    ## Code paths
    - `apps/web-console/app/auth/signup/...` — signup form + server action
    - `apps/web-console/app/auth/signin/...` — signin form
    - `apps/web-console/middleware.ts` — session gate
    - `apps/control-plane/internal/accounts/...` — account/membership provisioning

    ## Reproduce
    ```
    cd deploy/docker && docker compose --env-file ../../.env --profile local up --build
    # then visit http://localhost:3000/auth/signup
    ```

    ## Phase 02 summary references
    See `02-01-SUMMARY.md` (Supabase wiring) and `02-03-SUMMARY.md` (middleware + callback).
    ```

    Repeat for AUTH-02 (password reset + email verify routes), API-01..API-04 (point at Phase 06 SDK harness commands and chat/completions, embeddings, streaming, reasoning code paths in `apps/edge-api/internal/...`).

    For API-01..API-04 evidence files, prefer integration_tests entries that already exist in repo:
    ```
    - cd deploy/docker && docker compose --env-file ../../.env --profile test up --build
    - go test ./apps/edge-api/internal/inference/... -count=1 -short
    ```

    Constraint: this task touches NO code under `apps/`, `packages/`, `supabase/`. Only `.planning/` artifacts. If verification reveals AUTH-01/AUTH-02 are not actually satisfied by Phase 02 code, escalate via Blockers section in 11-VERIFICATION.md (Task 4) instead of marking Satisfied.
  </action>
  <verify>
    <automated>test -f .planning/REQUIREMENTS.md && for r in AUTH-01 AUTH-02 API-01 API-02 API-03 API-04; do test -f .planning/phases/11-verification-cleanup/evidence/$r.md || { echo missing $r; exit 1; }; done && grep -q "AUTH-01" .planning/REQUIREMENTS.md && grep -q "API-04" .planning/REQUIREMENTS.md</automated>
  </verify>
  <done>
    REQUIREMENTS.md exists at repo root with Evidence column. Six evidence files exist with valid YAML frontmatter (requirement_id, status, verified_at, verified_by, phase_satisfied, evidence block). Each evidence file references at least one runnable integration command and at least one code path. No production source files modified.
  </done>
</task>

<task type="auto">
  <name>Task 2: Audit UAT-REPORT.md against Phase 10 closure state</name>
  <files>.planning/UAT-REPORT.md</files>
  <action>
    Read `.planning/UAT-REPORT.md` (header dated 2026-04-13) and `.planning/phases/10-routing-storage-critical-fixes/10-UAT.md`. The 2026-04-13 report says "S3 Storage: Gracefully degraded" but Phase 10 closed storage wiring (Plans 10-07, 10-08, 10-11). Annotate, don't delete.

    Add a "## Post-Phase-10 Annotations" section at the bottom of UAT-REPORT.md listing each line item that is now stale:
    - S3 Storage line: now "Required at startup; live smoke completed Phase 10 Plan 11. See 10-UAT.md."
    - Any line about file/media/batch endpoints that Phase 10 changed.
    - Batch success path: still blocked upstream (per CLAUDE.md Known Issues #4) — confirm wording matches.

    Do NOT rewrite original entries. Append-only annotation preserves audit trail. Add header note at the top of the new section: "Annotations added by Phase 11 (2026-04-25) reconciling 2026-04-13 report against Phase 10 closure."

    For every annotated entry, also add an entry to that requirement's evidence file (Task 1) under a `## Known Caveats` heading so the matrix and the UAT report stay in sync.
  </action>
  <verify>
    <automated>grep -q "Post-Phase-10 Annotations" .planning/UAT-REPORT.md && grep -q "2026-04-25" .planning/UAT-REPORT.md</automated>
  </verify>
  <done>
    UAT-REPORT.md has a Post-Phase-10 Annotations section dated 2026-04-25 reconciling stale entries (storage degraded, file/media wiring, batch success path) against Phase 10 closure. Original 2026-04-13 entries are untouched.
  </done>
</task>

<task type="auto">
  <name>Task 3: Build scripts/verify-requirements-matrix.sh validator</name>
  <files>scripts/verify-requirements-matrix.sh</files>
  <action>
    Create executable shell script that:
    1. Reads `.planning/REQUIREMENTS.md`.
    2. Extracts every row that has an Evidence column with a markdown link of the form `[label](relative/path.md)` rooted under `.planning/`.
    3. Resolves the path relative to `.planning/` and asserts the file exists.
    4. Skips rows whose Evidence column says `Phase NN (planned)` or similar — those are intentional pending markers.
    5. For each existing evidence file, parses frontmatter and asserts: `requirement_id`, `status`, `verified_at`, `verified_by`, `evidence` keys are all present.
    6. Exits non-zero with a list of failures; exits 0 with a one-line success message ("OK: N evidence files validated") otherwise.

    Implementation notes:
    - Use `awk` + `grep` (no jq/yq dependency — keeps script runnable from a fresh checkout without extra installs).
    - Set `set -euo pipefail` at top.
    - Make script executable: `chmod +x scripts/verify-requirements-matrix.sh`.
    - Script is intended to run from repo root.
    - DO NOT change `apps/` or any production source.
  </action>
  <verify>
    <automated>chmod +x scripts/verify-requirements-matrix.sh && bash scripts/verify-requirements-matrix.sh</automated>
  </verify>
  <done>
    `scripts/verify-requirements-matrix.sh` exists, is executable, exits 0 against the REQUIREMENTS.md + evidence files produced in Task 1, and prints a one-line success summary listing N validated evidence files. Non-zero exit on any missing file or missing frontmatter key.
  </done>
</task>

<task type="auto">
  <name>Task 4: Write 11-VERIFICATION.md ship-gate log</name>
  <files>.planning/phases/11-verification-cleanup/11-VERIFICATION.md</files>
  <action>
    Produce phase verification log. Use the same format as `.planning/phases/10-routing-storage-critical-fixes/10-UAT.md`.

    Required sections:
    - Frontmatter: phase, verified_at, verified_by, status (closed|blocked).
    - `## Must-Have Truth Verification` — table with each must_have truth from this PLAN's frontmatter, the command run, captured output snippet, pass/fail.
    - `## Requirement Coverage` — list of requirement IDs touched (AUTH-01, AUTH-02, API-01..04), with their evidence-file path and validator exit code.
    - `## UAT Reconciliation` — confirmation that UAT-REPORT.md annotations are present.
    - `## Blockers` — explicit section. If during Task 1 audit any AUTH-* requirement was found NOT actually satisfied by Phase 02 code, list it here with the missing piece. Empty section is acceptable but the heading must exist.
    - `## v1.1.0 Ship-Gate Mapping` — explicit statement of which Master Plan ship-gate item this phase closes (per V1.1-MASTER-PLAN.md §Cross-Phase Concerns: "all v1.0 requirements have green verification artifacts").

    Run validator from Task 3 and paste exit-code + summary line into the verification log. Run the per-requirement integration command from one evidence file (cheapest: `go test ./apps/edge-api/internal/inference/... -count=1 -short` via Docker toolchain) and paste the head of its output as additional evidence — but if Docker is not available in the execution environment, mark that line "deferred to executor" rather than fabricating output.
  </action>
  <verify>
    <automated>test -f .planning/phases/11-verification-cleanup/11-VERIFICATION.md && grep -q "Must-Have Truth Verification" .planning/phases/11-verification-cleanup/11-VERIFICATION.md && grep -q "Blockers" .planning/phases/11-verification-cleanup/11-VERIFICATION.md && grep -q "Ship-Gate" .planning/phases/11-verification-cleanup/11-VERIFICATION.md</automated>
  </verify>
  <done>
    11-VERIFICATION.md exists with Must-Have Truth Verification table, Requirement Coverage, UAT Reconciliation, Blockers, and v1.1.0 Ship-Gate Mapping sections. Validator output recorded. Status set to `closed` only if Blockers section is empty; otherwise `blocked` with explicit list.
  </done>
</task>

</tasks>

<verification>
Phase-level verification commands (run after all tasks complete):

1. `test -f .planning/REQUIREMENTS.md` — active matrix exists.
2. `bash scripts/verify-requirements-matrix.sh` — exits 0.
3. `ls .planning/phases/11-verification-cleanup/evidence/ | wc -l` — at least 6 evidence files (AUTH-01, AUTH-02, API-01..04).
4. `grep -c "Post-Phase-10 Annotations" .planning/UAT-REPORT.md` — equals 1.
5. `grep -c "phase_satisfied" .planning/phases/11-verification-cleanup/evidence/*.md | awk -F: '{s+=$2} END {exit !(s>=6)}'` — every evidence file has the field.
6. `git diff --name-only` reports zero changes under `apps/`, `packages/`, `supabase/`, `deploy/`.

Expected outputs:
- (2) prints `OK: 6 evidence files validated`.
- (6) prints empty (no production code changes).
</verification>

<success_criteria>
Definition of Done — also serves as v1.1.0 ship-gate input for this phase:

- [ ] `.planning/REQUIREMENTS.md` exists, lists every v1.0 requirement, and adds an Evidence column resolving to on-disk evidence files for satisfied items.
- [ ] AUTH-01 + AUTH-02 are wired to Phase 02 code paths via evidence files (no longer "Pending — Deferred v1.1").
- [ ] API-01..API-04 evidence files include frontmatter (requirement_id, status, verified_at, verified_by, phase_satisfied, evidence) and at least one runnable integration command.
- [ ] `.planning/UAT-REPORT.md` has a Post-Phase-10 Annotations section reconciling stale 2026-04-13 entries against Phase 10 closure — original entries preserved.
- [ ] `scripts/verify-requirements-matrix.sh` exists, is executable, exits 0 against the produced matrix, and is suitable for CI.
- [ ] `.planning/phases/11-verification-cleanup/11-VERIFICATION.md` records pass/fail for every must_have truth, lists Blockers (or empty), and explicitly maps to V1.1-MASTER-PLAN.md ship-gate item "all v1.0 requirements have green verification artifacts".
- [ ] Zero production source files modified (`apps/`, `packages/`, `supabase/`, `deploy/` clean per `git diff --name-only`).
- [ ] Branch `a/phase-11-verification-cleanup` created and PR opened against main (single PR for the phase per V1.1-MASTER-PLAN.md branching strategy).

Ship-gate mapping: closes the v1.1.0 ship-gate item "all v1.0 requirements have green verification artifacts." Does NOT close FX/USD audit (Phase 17) or anti-abuse audit (Phase 21) — those gate separately.
</success_criteria>

<blockers>
Discovered during planning (2026-04-25):

1. **Active `.planning/REQUIREMENTS.md` does not exist.** Only the archived `.planning/milestones/v1.0-REQUIREMENTS.md` is on disk. The original Phase 11 prompt and CLAUDE.md both reference `.planning/REQUIREMENTS.md` as if it were live. Plan recreates it as Task 1 — recording here so the executor knows the file is being created, not edited.

2. **Archived matrix marks AUTH-01..AUTH-04 as "Pending — Deferred v1.1"** even though Phase 02 SUMMARY artifacts indicate signup, signin, email verify, password reset, and middleware session gating all shipped. Task 1 must verify Phase 02 code actually satisfies AUTH-01 + AUTH-02 (skim Phase 02 SUMMARY files); if a piece is missing the executor must list it in 11-VERIFICATION.md Blockers rather than fabricating Satisfied status. AUTH-03 + AUTH-04 explicitly stay Pending and route to a future phase — they are out of scope for Phase 11.

3. **Phase folder ambiguity.** Two empty directories exist: `.planning/phases/11-verification-cleanup/` (this plan's home, per user prompt) and `.planning/phases/11-compliance-verification-cleanup/` (older naming). Plan writes to `11-verification-cleanup/`. The other directory should be deleted by the executor as part of the phase close (note in 11-VERIFICATION.md UAT Reconciliation section).

4. **UAT-REPORT.md is the v1.0 milestone-level report, not a phase-scoped one.** Annotation-only approach (Task 2) preserves audit trail. Full rewrite is intentionally out of scope.
</blockers>

<output>
After completion, create `.planning/phases/11-verification-cleanup/11-01-SUMMARY.md` per the GSD summary template, recording:
- Files created
- Files modified
- Validator output
- Any Blockers carried forward
- Ship-gate status update for v1.1.0
</output>
