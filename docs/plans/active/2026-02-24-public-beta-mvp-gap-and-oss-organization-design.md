# Public Beta MVP Gap Analysis and OSS Organization Design

Date: 2026-02-24
Status: Active - OSS governance docs delivered via issue #5 / PR #39; broader backlog hygiene remains active
Scope: Gap analysis, architecture decision narrative, GitHub issue/label setup, and OSS contributor-facing repo organization (docs/meta only)

## Context

The repository already has a strong implementation baseline:

- TypeScript monorepo with API (`apps/api`) and web (`apps/web`)
- Provider adapter/registry architecture with fallback
- Public and internal provider status endpoint split
- Billing and ledger policy with explicit business rules
- Categorized docs structure under `docs/`

The immediate need is to evaluate readiness for a **Public Beta MVP**, identify concrete gaps, and convert those gaps into a maintainable GitHub issue system while improving open-source contributor experience.

## Goals

1. Produce an evidence-based gap analysis against Public Beta MVP expectations.
2. Document architecture decisions and tradeoffs for current system direction.
3. Organize backlog in GitHub issues with consistent labels and milestones.
4. Improve repository appeal and usability for open-source contributors.

## Non-Goals

- No code-tree refactors or directory moves of API/web runtime paths.
- No architecture migration in this phase.
- No billing formula changes.
- No endpoint contract removals or renames.

## Design Decisions

### 1) MVP Target Definition

Use **Public Beta MVP** as the quality bar (not internal demo, not full production launch).

Implication:

- Prioritize reliability, security, billing correctness, and contributor onboarding.
- Defer non-blocking expansion work to post-MVP milestones.

### 2) Gap Analysis Method

Apply a weighted rubric:

- Core Product Fit: 20%
- Financial Correctness: 20%
- Security and Access Control: 20%
- Reliability and Provider Operations: 20%
- Operability and OSS Readiness: 20%

Per control scoring:

- `0` = missing
- `0.5` = partial
- `1` = meets Public Beta MVP bar

Outputs:

- readiness score
- distance to MVP (`100 - readiness`)
- blockers and recommended issue sequence

### 3) Architecture Decision Narrative

Create an ADR-style architecture decision summary that captures why current architecture is valid for Public Beta, including accepted tradeoffs:

- TypeScript monorepo as active implementation path
- provider adapter + registry as canonical pattern
- public/internal provider status boundary
- prepaid credit and ledger policy boundaries
- Supabase Option A migration trajectory

### 4) GitHub Backlog Operating Model

Define a scalable label taxonomy:

- `kind:*`: feature, bug, hardening, docs, chore
- `area:*`: api, web, providers, billing, auth, ops, docs, oss
- `priority:*`: P0, P1, P2, P3
- `risk:*`: security, financial, availability, compliance
- `status:*`: needs-triage, blocked, ready, in-progress
- contributor labels: `good first issue`, `help wanted`

Define milestones:

- `MVP Public Beta - Blockers`
- `MVP Public Beta - Stabilization`
- `Post-MVP - Enhancements`

Issue requirements:

- one primary `kind:*`
- one primary `area:*`
- one `priority:*`
- explicit acceptance criteria and verification steps

### 5) OSS-Friendly Repo Organization (Respect Existing Doc Categories)

Keep existing docs information architecture intact:

- `docs/architecture`
- `docs/design`
- `docs/plans`
- `docs/runbooks`
- `docs/release`
- `docs/engineering`

Only add and link contributor-facing assets:

- root: `CONTRIBUTING.md`, `CODE_OF_CONDUCT.md`, `SECURITY.md`, `SUPPORT.md`, `GOVERNANCE.md`
- `.github/`: `CODEOWNERS`
- `.github/`: issue templates, PR template, labels bootstrap helper, optional CI workflow updates
- docs cross-links: update `README.md` and `docs/README.md` without category churn

## Execution Plan (High Level)

1. Prepare gap analysis report and MVP scorecard.
2. Draft architecture decision document.
3. Add `.github` templates and label plan.
4. Create prioritized GitHub issues from gap outputs.
5. Add OSS community files and contributor onboarding updates.
6. Validate links, commands, and doc consistency.

## Risks and Mitigations

- Risk: Backlog inflation with low-value issues.
  - Mitigation: Require MVP-risk linkage for P0/P1.
- Risk: Documentation drift from implementation.
  - Mitigation: Use acceptance criteria tied to current paths and commands.
- Risk: Contributor confusion from doc duplication.
  - Mitigation: Keep category structure unchanged and add clear cross-links.

## Validation Criteria

- Gap report includes weighted score and ranked blockers.
- Architecture decision is explicit about tradeoffs and boundaries.
- GitHub labels/milestones/issues are consistent and actionable.
- New contributors can onboard via root community docs and linked run commands.
- Existing documentation categories remain intact.

## Progress Update

- Delivered on 2026-03-12 via issue `#5` / PR `#39`:
  - `CONTRIBUTING.md`
  - `CODE_OF_CONDUCT.md`
  - `SECURITY.md`
  - `SUPPORT.md`
  - `GOVERNANCE.md`
  - `.github/CODEOWNERS`
  - `README.md`, `docs/README.md`, and `CHANGELOG.md` cross-links
- Remaining work in this design track is backlog and repository-governance hygiene outside the root policy-doc set.
