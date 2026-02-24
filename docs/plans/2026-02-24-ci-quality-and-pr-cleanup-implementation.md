# CI Quality and PR Cleanup Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Add low-cost monorepo CI quality gates, automate safe PR post-merge cleanup, and track rollout in GitHub using repository issue conventions.

**Architecture:** Add two GitHub workflows: one monorepo quality workflow and one PR-close cleanup workflow. Use a repository script for cleanup logic so policy is versioned and testable. Enforce issue metadata hygiene via labels/milestone and document conventions in AGENTS guidance.

**Tech Stack:** GitHub Actions, bash, gh CLI, pnpm, Node 24 (CI runtime uses Node 24)

---

## Task 1: Add monorepo CI workflow

**Files:**
- Create: `.github/workflows/ci.yml`

### Step 1: Define triggers and cost controls
- Add `pull_request` and `push` triggers on `main`.
- Add `paths-ignore` for docs-only updates.
- Add `concurrency` with `cancel-in-progress: true`.

### Step 2: Add setup and caching
- Configure checkout, pnpm setup, Node setup, and pnpm cache.

### Step 3: Add quality checks
- Run API lint, web lint, API tests, web tests, API build, web build.

### Step 4: Validate workflow syntax
Run: `pnpm --filter @hive/api test`
Expected: PASS (sanity check for changed repository state).

## Task 2: Add post-merge cleanup workflow + script

**Files:**
- Create: `.github/workflows/pr-cleanup.yml`
- Create: `.github/scripts/pr-cleanup.sh`

### Step 1: Add merged-PR trigger
- Trigger on `pull_request.closed` with merge condition.

### Step 2: Implement safe cleanup script
- Parse event payload.
- Guard default branch and fork branches.
- Delete merged source branch using `gh api` only when safe.
- Remove `status:in-progress` PR label if present.

### Step 3: Wire workflow to script
- Ensure script executes with GitHub token.
- Ensure script is executable.

## Task 3: Create tracking issue with full planning context

**Files:**
- Modify: none (remote GitHub state)

### Step 1: Select required metadata
- Labels: `kind:hardening`, `area:ops`, `priority:P1`, `status:ready`.
- Milestone: `MVP Public Beta - Stabilization`.

### Step 2: Create issue with required sections
- Include Context, Problem, Why this matters, Acceptance Criteria, Verification, Dependencies.
- Include rollout plan and optimization strategy.

### Step 3: Attempt project assignment if permitted
- If token lacks project scope, record limitation and continue.

## Task 4: Update AGENTS issue-hygiene guidance

**Files:**
- Modify: `AGENTS.md`

### Step 1: Add explicit GitHub issue hygiene section
- Document required label matrix and milestone usage.
- Document project assignment expectation when scope allows.
- Document required issue body sections.

## Task 5: Verify changed workflows and repo status

**Files:**
- Modify: none

### Step 1: Run API tests (required by repository standards)
Run: `pnpm --filter @hive/api test`
Expected: PASS.

### Step 2: Run API build (required by repository standards)
Run: `pnpm --filter @hive/api build`
Expected: PASS.

### Step 3: Optionally run web build for CI parity confidence
Run: `pnpm --filter @hive/web build`
Expected: PASS.

### Step 4: Verify git state
Run: `git status --short`
Expected: only intended files changed.
