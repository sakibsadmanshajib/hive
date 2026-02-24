# Public Beta MVP Gap and OSS Organization Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Produce a Public Beta MVP gap analysis, document architecture decisions, create GitHub issue/label operating structure, and improve contributor-facing repository organization without changing code-tree layout.

**Architecture:** Keep the current TypeScript monorepo and existing docs category taxonomy unchanged. Add assessment artifacts under current docs categories, add GitHub workflow metadata under `.github/`, and use `gh` CLI for labels/milestones/issues. Prioritize P0/P1 work by MVP risk and preserve public/internal provider-status security boundaries.

**Tech Stack:** Markdown docs, GitHub templates/YAML, GitHub CLI (`gh`), pnpm workspace scripts

---

### Task 1: Baseline Repository and GH State Snapshot

**Files:**
- Modify: none
- Test: none

**Step 1: Capture repo status**

Run: `git status --short`
Expected: current dirty files list (no changes made yet).

**Step 2: Capture existing labels**

Run: `gh label list --limit 200`
Expected: label list or empty/minimal defaults.

**Step 3: Capture existing milestones**

Run: `gh api repos/:owner/:repo/milestones --paginate`
Expected: JSON response with existing milestones.

**Step 4: Commit**

Do not commit in this task.

### Task 2: Create Public Beta MVP Gap Analysis Report

**Files:**
- Create: `docs/architecture/public-beta-mvp-gap-analysis.md`
- Test: `apps/api/test/routes/providers-status-route.test.ts` (reference behavior constraints)

**Step 1: Write the report skeleton**

Create sections:

```markdown
# Public Beta MVP Gap Analysis
## Scope and MVP Bar
## Weighted Rubric
## Current Scorecard
## Top Blockers
## Recommended Issue Sequence
## Distance to MVP
```

**Step 2: Add weighted rubric and scoring method**

Use explicit weights:

```markdown
- Core Product Fit (20%)
- Financial Correctness (20%)
- Security and Access Control (20%)
- Reliability and Provider Operations (20%)
- Operability and OSS Readiness (20%)
```

**Step 3: Add current-state scoring with evidence links**

Reference concrete files and docs:

- `apps/api/src/providers/registry.ts`
- `apps/api/src/routes/providers-status.ts`
- `docs/architecture/system-architecture.md`
- `docs/plans/active/future-implementation-roadmap.md`

**Step 4: Run targeted test to ensure security boundary baseline remains valid**

Run: `pnpm --filter @bd-ai-gateway/api test -- apps/api/test/routes/providers-status-route.test.ts`
Expected: PASS.

**Step 5: Commit**

```bash
git add docs/architecture/public-beta-mvp-gap-analysis.md
git commit -m "docs(architecture): add public beta MVP gap analysis"
```

### Task 3: Write Architecture Decision Record for MVP Readiness

**Files:**
- Create: `docs/architecture/adr-2026-02-public-beta-mvp-readiness.md`
- Test: none

**Step 1: Write ADR header and decision statement**

```markdown
# ADR: Public Beta MVP Readiness Strategy
## Status
Accepted
## Context
## Decision
## Consequences
```

**Step 2: Document accepted architecture decisions**

Include:

- TS monorepo is source of truth
- Provider adapter/registry remains canonical
- Public/internal provider status split remains mandatory
- Credit/ledger policy boundaries preserved
- Supabase Option A remains migration direction

**Step 3: Document deferred items and rationale**

List post-MVP deferrals and risk guardrails.

**Step 4: Commit**

```bash
git add docs/architecture/adr-2026-02-public-beta-mvp-readiness.md
git commit -m "docs(architecture): record public beta readiness decisions"
```

### Task 4: Add Contributor Community Files at Repository Root

**Files:**
- Create: `CONTRIBUTING.md`
- Create: `CODE_OF_CONDUCT.md`
- Create: `SECURITY.md`
- Create: `SUPPORT.md`
- Test: none

**Step 1: Create `CONTRIBUTING.md` with local setup and PR workflow**

Include exact commands:

```markdown
pnpm install
pnpm --filter @bd-ai-gateway/api test
pnpm --filter @bd-ai-gateway/api build
pnpm --filter @bd-ai-gateway/web build
```

**Step 2: Create `SECURITY.md` with vulnerability disclosure process**

Define safe disclosure and sensitive endpoint expectations.

**Step 3: Create `CODE_OF_CONDUCT.md` and `SUPPORT.md`**

Add behavior expectations and support channels.

**Step 4: Commit**

```bash
git add CONTRIBUTING.md CODE_OF_CONDUCT.md SECURITY.md SUPPORT.md
git commit -m "docs(oss): add community and contribution policies"
```

### Task 5: Add GitHub Issue and PR Templates

**Files:**
- Create: `.github/ISSUE_TEMPLATE/bug_report.yml`
- Create: `.github/ISSUE_TEMPLATE/feature_request.yml`
- Create: `.github/ISSUE_TEMPLATE/documentation_task.yml`
- Create: `.github/ISSUE_TEMPLATE/mvp_gap.yml`
- Create: `.github/ISSUE_TEMPLATE/config.yml`
- Create: `.github/pull_request_template.md`
- Test: none

**Step 1: Create MVP gap issue template**

Template fields:

- gap/problem
- MVP risk type
- acceptance criteria
- verification commands
- dependencies

**Step 2: Create bug/feature/docs templates**

Keep each template concise and structured for triage.

**Step 3: Create PR template**

Include checklist for tests/build/docs/security impact.

**Step 4: Commit**

```bash
git add .github/ISSUE_TEMPLATE .github/pull_request_template.md
git commit -m "chore(github): add issue and PR templates"
```

### Task 6: Define and Apply Label Taxonomy

**Files:**
- Create: `.github/labels.json`
- Modify: none
- Test: none

**Step 1: Define label set in JSON**

Include the agreed taxonomy:

- `kind:*`
- `area:*`
- `priority:*`
- `risk:*`
- `status:*`
- `good first issue`, `help wanted`

**Step 2: Apply labels via `gh` CLI**

Run:

```bash
jq -c '.[]' .github/labels.json | while read -r label; do
  name=$(echo "$label" | jq -r '.name')
  color=$(echo "$label" | jq -r '.color')
  desc=$(echo "$label" | jq -r '.description')
  gh label create "$name" --color "$color" --description "$desc" --force
done
```

Expected: labels created/updated successfully.

**Step 3: Verify labels**

Run: `gh label list --limit 200`
Expected: all planned labels present.

**Step 4: Commit**

```bash
git add .github/labels.json
git commit -m "chore(github): codify repository label taxonomy"
```

### Task 7: Create MVP Milestones

**Files:**
- Modify: none
- Test: none

**Step 1: Create blocker milestone**

Run:

```bash
gh api repos/:owner/:repo/milestones -f title='MVP Public Beta - Blockers' -f state='open' -f description='P0 issues required before public beta'
```

**Step 2: Create stabilization milestone**

Run:

```bash
gh api repos/:owner/:repo/milestones -f title='MVP Public Beta - Stabilization' -f state='open' -f description='P1 hardening during beta rollout'
```

**Step 3: Create post-MVP milestone**

Run:

```bash
gh api repos/:owner/:repo/milestones -f title='Post-MVP - Enhancements' -f state='open' -f description='P2+ deferred enhancements'
```

**Step 4: Verify milestones**

Run: `gh api repos/:owner/:repo/milestones --paginate`
Expected: three milestones visible.

### Task 8: Bootstrap Gap-Derived GitHub Issues

**Files:**
- Create: `docs/plans/active/public-beta-mvp-issue-seed.md`
- Modify: none
- Test: none

**Step 1: Draft issue seed list from gap report**

Use a table with:

- title
- kind
- area
- priority
- risk
- milestone

**Step 2: Create P0/P1 issues via `gh issue create`**

Use standardized body sections:

- Context
- Gap / Problem
- Why this matters for MVP
- Acceptance criteria
- Verification
- Dependencies

**Step 3: Verify issue metadata**

Run: `gh issue list --limit 100 --state open`
Expected: created issues with expected labels and milestones.

**Step 4: Commit seed artifact**

```bash
git add docs/plans/active/public-beta-mvp-issue-seed.md
git commit -m "docs(plans): add public beta issue seeding matrix"
```

### Task 9: Update Readme Entrypoints Without Changing Category Structure

**Files:**
- Modify: `README.md`
- Modify: `docs/README.md`
- Create: `docs/engineering/open-source-contributor-guide.md`
- Test: none

**Step 1: Add OSS quickstart section to root README**

Include pointers to:

- `CONTRIBUTING.md`
- issue templates and labels policy
- docs index

**Step 2: Add contributor guide under engineering category**

Document triage labels, milestone usage, and issue lifecycle.

**Step 3: Update docs index links**

Reference the new contributor guide while preserving existing category sections.

**Step 4: Commit**

```bash
git add README.md docs/README.md docs/engineering/open-source-contributor-guide.md
git commit -m "docs(oss): add contributor entrypoints and onboarding guide"
```

### Task 10: Verification Pass

**Files:**
- Modify: none
- Test: repo checks

**Step 1: Run API tests**

Run: `pnpm --filter @bd-ai-gateway/api test`
Expected: PASS.

**Step 2: Run API build**

Run: `pnpm --filter @bd-ai-gateway/api build`
Expected: PASS.

**Step 3: Run web build**

Run: `pnpm --filter @bd-ai-gateway/web build`
Expected: PASS.

**Step 4: Run final git status**

Run: `git status --short`
Expected: clean tree if all commits were created, or only intentional uncommitted files.

### Task 11: Optional Follow-Up (Maintainer UX)

**Files:**
- Create: `.github/workflows/ci.yml` (if absent)
- Test: workflow syntax only

**Step 1: Add lightweight PR checks workflow**

Run on pull requests:

- API test
- API build
- Web build

**Step 2: Validate workflow syntax**

Run: `gh workflow list`
Expected: workflow appears once pushed.

**Step 3: Commit**

```bash
git add .github/workflows/ci.yml
git commit -m "ci: add baseline PR validation workflow"
```
