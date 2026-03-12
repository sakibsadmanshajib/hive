# Governance

## Governance Model

Hive uses a lightweight maintainer-led governance model.

This means:

- contributors can propose changes through issues and pull requests
- maintainers review changes, make merge decisions, and manage releases
- governance may evolve later into a named core team without changing the basic contribution model

## Roles

### Contributors

Contributors may:

- open issues
- propose documentation, code, test, or tooling changes
- participate in technical review and design discussion

Contributors are expected to follow:

- `CONTRIBUTING.md`
- `CODE_OF_CONDUCT.md`
- `SECURITY.md`

### Maintainers

Maintainers are responsible for:

- triaging issues and pull requests
- protecting API stability and public/internal security boundaries
- reviewing changes for billing correctness, auth safety, and production risk
- keeping docs and release notes aligned with behavior
- deciding when changes are ready to merge or release

## Decision-Making

The default decision path is:

1. discuss the problem in an issue, plan, or pull request
2. review the proposed change against current requirements and repository policy
3. merge when the change is technically sound, appropriately verified, and documented

Maintainers have final decision authority on:

- merge approval
- release readiness
- repository policy changes
- issue prioritization when tradeoffs are required

## Higher-Scrutiny Change Areas

Changes in the following areas require especially careful review:

- billing, credits, refunds, and ledger behavior
- authentication, API keys, and session handling
- public versus internal provider status and metrics boundaries
- security-sensitive configuration or disclosure behavior

For these areas, maintainers should expect stronger verification and may require narrower, more reviewable changes.

## Policy Changes

Governance and repository policy documents may change over time.

When changing governance-related files:

- explain the reason clearly
- update linked docs in the same change
- record notable policy changes in `CHANGELOG.md`

## Escalation and Conduct

- behavior concerns are handled through `CODE_OF_CONDUCT.md`
- security concerns are handled through `SECURITY.md`
- general project help and support routing are documented in `SUPPORT.md`

## Future Evolution

If Hive adds more maintainers or a formal core team later, this document can be expanded with named roles and voting or approval rules. Until then, the project intentionally keeps governance simple and role-based.
