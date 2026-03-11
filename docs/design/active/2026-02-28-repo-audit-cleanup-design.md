# Repo Audit and Cleanup Design

Date: 2026-02-28
Status: Proposed

## Context

This session targets a full repository audit for an in-progress MVP with explicit permission for disruptive cleanup and no production data retention constraints.

Primary objective:
- establish a fact-based inventory of implemented vs claimed behavior,
- remove redundant/stale code and docs,
- update documentation and contracts to match reality,
- include a concrete migration/removal proposal for the legacy Python MVP reference implementation.

## Evidence Summary (Current State)

Validated from repository code, tests/builds, and GitHub issues/PR comments:

- Baseline health:
  - `pnpm --filter @hive/api test` passes (68 tests).
  - `pnpm --filter @hive/api build` passes.
  - `pnpm --filter @hive/web build` passes.
- Major drift exists between claims and implementation:
  - README lists auth/2FA routes that do not match actual route paths.
  - README claims `GET /v1/auth/session`, but no corresponding route exists.
  - OpenAPI contract is duplicated and divergent:
    - `openapi/openapi.yaml`
    - `packages/openapi/openapi.yaml`
  - Header contract drift exists (`x-routing-policy-version`, `x-estimated-credits` in one spec but not runtime behavior).
- Documentation duplication/drift:
  - `docs/plans/<file>.md` and `docs/plans/active/<file>.md` contain overlapping but non-identical versions.
- GitHub backlog drift:
  - some open issues represent delivered work or conflict with merged direction (example: OpenRouter issue remains open after removal PR merged).
- Legacy implementation remains:
  - `app/` and `tests/` Python MVP reference code remains in tree and contributes to ambiguity.

## Approaches

### Approach A: Conservative Alignment Only

Scope:
- Keep legacy Python reference code.
- Fix docs, issue hygiene, and contract drift only.

Pros:
- Lowest immediate risk.
- Minimal file churn.

Cons:
- Leaves major redundancy in place.
- Continues cognitive overhead and maintenance ambiguity.

### Approach B: Aggressive One-Shot Cleanup

Scope:
- Remove legacy Python MVP code in same pass as docs/contracts cleanup.
- Collapse duplicate OpenAPI/doc artifacts immediately.
- Re-triage backlog in one execution wave.

Pros:
- Fastest path to a clean TS-only codebase.
- Eliminates most ambiguity quickly.

Cons:
- High blast radius.
- Harder review/debug if multiple categories change at once.

### Approach C: Staged Aggressive Cleanup (Recommended)

Scope:
- Phase 1: audit matrix, contract/doc fixes, GitHub triage.
- Phase 2: remove/migrate legacy Python MVP and redundant artifacts with explicit migration notes.
- Phase 3: verification sweep and final docs/changelog sync.

Pros:
- Still converges to a clean TS-only state.
- Better control of regressions and reviewability.
- Clear checkpoints and rollback points.

Cons:
- Slightly slower than one-shot cleanup.

## Recommendation

Use Approach C (staged aggressive cleanup).

Rationale:
- The maintainer has explicitly allowed disruptive changes with no data-retention constraints.
- The largest risk is not runtime breakage today; it is uncontrolled cleanup that mixes too many concerns at once.
- Staging gives strong momentum while preserving traceability and fast rollback if needed.

## Legacy Python MVP Removal/Migration Proposal

Target state:
- TypeScript monorepo is the only implementation path.
- Python MVP code is removed from root runtime/test surfaces.

Migration design:
1. Create a final archived reference note in docs (`docs/archive` or `docs/architecture`) that maps former Python modules to TS equivalents.
2. Remove `app/` and root `tests/` Python MVP files.
3. Remove Python-specific mentions from primary quickstart/runtime docs, but keep a short historical note.
4. Ensure CI/tooling no longer references Python components (if any latent references exist).

This is intentionally destructive and acceptable for this MVP stage.

## Success Criteria

- Claims match implementation for routes, headers, provider status behavior, and auth flow.
- Exactly one canonical OpenAPI contract path remains.
- Plan/docs structure is simplified and internally consistent.
- GitHub issue/PR backlog reflects reality (delivered vs pending).
- Legacy Python MVP implementation is either removed or explicitly documented as archived with no runtime ambiguity.

## Risks and Mitigation

- Risk: accidental removal of still-needed references.
  - Mitigation: staged removal with pre/post verification, and historical mapping doc.
- Risk: docs/contract churn causes temporary confusion.
  - Mitigation: define canonical sources and remove duplicates in same change wave.
- Risk: backlog triage introduces governance mismatch.
  - Mitigation: tie each closure/update to code evidence and merged PR links.

## Verification Strategy

- API: `pnpm --filter @hive/api test` and `pnpm --filter @hive/api build`
- Web: `pnpm --filter @hive/web test` and `pnpm --filter @hive/web build`
- Route/contract checks:
  - route inventory vs README/OpenAPI diff checks.
  - provider status public/internal boundary checks in tests.
- Optional integration pass (if needed in execution phase): docker compose + smoke e2e.

