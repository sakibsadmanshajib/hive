# Issue #5 OSS Governance Docs Design

Date: 2026-03-12
Status: Implemented in PR #39
Issue: `#5` - Publish OSS governance docs and contribution policy set

## Progress

- Design approved in-session on 2026-03-12
- Implemented on branch `docs/issue-5-oss-governance-docs`
- PR opened: `#39`

## Goal

Close the public-beta governance/compliance gap by adding a complete but lightweight open-source policy set that matches the repository's current operating model without inventing a larger maintainer structure than exists today.

## Scope

Add and link the following repository policy files:

- `CONTRIBUTING.md`
- `CODE_OF_CONDUCT.md`
- `SECURITY.md`
- `SUPPORT.md`
- `GOVERNANCE.md`
- `.github/CODEOWNERS`

Update supporting discovery docs:

- `README.md`
- `docs/README.md`
- `CHANGELOG.md`

## Design Principles

- Keep governance role-based rather than person-based.
- Reflect the repo's real workflow from `AGENTS.md`, existing docs, and current verification commands.
- Prefer lightweight policy language over enterprise process theater.
- Keep contributor and operator guidance discoverable from the repository root.
- Satisfy issue verification with explicit checks instead of inventing runtime tests for doc-only behavior.

## Document Behavior

### CONTRIBUTING

Document local setup, repository structure, branch and PR expectations, testing/build verification, documentation discipline, and the requirement to keep behavior changes, tests, and docs aligned.

### CODE_OF_CONDUCT

Provide a standard contributor behavior baseline and a neutral reporting path suitable for a project that may add named maintainers later.

### SECURITY

Separate private vulnerability reporting from public issue reporting, prohibit posting secrets publicly, and state high-level maintainer handling expectations.

### SUPPORT

Route users to the correct channel for usage questions, bug reports, feature requests, and security concerns. Clarify that support is best-effort and scoped to the open-source project.

### GOVERNANCE

Define a lightweight maintainer-led model with future-friendly roles:

- contributors can propose changes
- maintainers review, merge, release, and interpret policy
- governance can later expand to a named core team without replacing the policy model
- sensitive areas such as billing, auth, and provider boundary changes require stricter review

### CODEOWNERS

Provide broad ownership coverage for current repo areas and policy files, avoiding fake specialization while ensuring review routing exists.

## Verification Strategy

Use explicit verification checks appropriate for docs/meta work:

- confirm all required files exist
- confirm `README.md` and `docs/README.md` link to the new policy set
- inspect `CODEOWNERS` coverage for major repo areas
- run a web build as a repo-wide sanity check because root docs change but no runtime code changes are expected

## Risks and Mitigations

- Risk: policy text drifts from actual workflow
  - Mitigation: anchor contribution rules to current `AGENTS.md` and existing project commands
- Risk: governance language overstates current maintainer structure
  - Mitigation: use role-based language without naming a core team
- Risk: contributor discoverability remains weak
  - Mitigation: add root and docs index links in the same change

## Non-Goals

- No runtime API or web behavior changes
- No GitHub issue template or workflow expansion in this issue
- No named maintainer roster unless requested later
