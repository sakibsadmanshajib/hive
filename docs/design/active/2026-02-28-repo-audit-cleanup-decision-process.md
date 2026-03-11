# Repo Audit and Cleanup Decision Process

Date: 2026-02-28
Status: Recorded

## Decision Context

The repository currently contains drift across implementation, docs/contracts, and backlog tracking, plus a retained legacy Python MVP reference path (`app/`, root `tests/`) that increases ambiguity.

Session constraints confirmed by maintainer:
- still MVP stage,
- disruptive refactors are acceptable,
- no data retention constraints,
- prefer momentum with a concrete recovery path over conservative stabilization.

## Options Considered

1. Conservative alignment only
- Keep legacy Python reference untouched.
- Fix docs/contracts/backlog metadata only.

2. Aggressive one-shot cleanup
- Remove legacy Python and resolve all known drift in one pass.

3. Staged aggressive cleanup
- Preserve aggressive direction, but split execution into verifiable phases.

## Decision

Chosen option: **Staged aggressive cleanup**.

Reasoning:
- Achieves target state (clean TS-only implementation direction) without one-commit blast radius.
- Provides clear checkpoints for verification and rollback.
- Supports fast execution while still allowing evidence-based GitHub triage and contract alignment.

## Scope Decisions

Included:
- claims-vs-implementation audit matrix,
- OpenAPI/doc canonicalization,
- route/header contract reconciliation,
- docs redundancy cleanup,
- GitHub issue/PR triage alignment,
- legacy Python MVP migration map and removal plan.

Initially deferred from this tracking PR:
- broad functional contract changes beyond the verified cleanup scope.

## Canonical Doc Placement for This Track

- Design artifact: `docs/design/active/2026-02-28-repo-audit-cleanup-design.md`
- Decision process log: `docs/design/active/2026-02-28-repo-audit-cleanup-decision-process.md`
- Execution plan artifact (tracked copy): `docs/plans/2026-02-28-repo-audit-cleanup-plan.md`

## Next Trigger

Continue execution against the tracked plan doc and keep status/current-scope notes in `docs/` as the cleanup branch evolves.
