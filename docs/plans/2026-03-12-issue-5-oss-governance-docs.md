# Issue #5 OSS Governance Docs Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Add a lightweight but complete open-source governance and contribution policy set for issue `#5`, with clear contributor/operator discovery and explicit verification.

**Architecture:** This is a docs-and-repo-metadata change only. Root policy files define contributor, conduct, security, support, and governance expectations; `.github/CODEOWNERS` provides review routing; `README.md`, `docs/README.md`, and `CHANGELOG.md` surface the policy set. No runtime behavior or API contracts change.

**Tech Stack:** Markdown, GitHub `CODEOWNERS`, pnpm workspace commands

---

## Goal

Implement issue `#5` by adding the full OSS governance document set, making it discoverable from existing docs entry points, and verifying the repository still builds cleanly after the documentation update.

## Assumptions

- The repo currently has no root OSS governance policy files beyond `README.md` and `CHANGELOG.md`.
- Governance should remain role-based, not tied to named people.
- Explicit verification checks are acceptable for this docs-heavy issue.
- The preferred plan writer helper is unavailable in this repo, so this plan is written directly to `docs/plans/`.

## Plan

### Step 1

**Files:** `README.md`, `AGENTS.md`, `docs/README.md`, `CHANGELOG.md`, existing docs index files as needed

**Change:** Re-read the current repository guidance and docs entry points, then extract the exact workflow, verification commands, and documentation conventions that the new policy files must mirror.

**Verify:** `rg -n "pnpm --filter|docs discipline|CHANGELOG|CODEOWNERS|security|support" README.md AGENTS.md docs/README.md CHANGELOG.md`

### Step 2

**Files:** `CONTRIBUTING.md`

**Change:** Create `CONTRIBUTING.md` covering setup, repo structure, issue/PR workflow, verification expectations, doc-update discipline, and the current repo-specific contribution rules.

**Verify:** `sed -n '1,240p' CONTRIBUTING.md`

### Step 3

**Files:** `CODE_OF_CONDUCT.md`, `SECURITY.md`, `SUPPORT.md`

**Change:** Add the behavior, disclosure, and support policy documents with concise, repo-appropriate guidance and clear routing between public issues and private security reporting.

**Verify:** `sed -n '1,220p' CODE_OF_CONDUCT.md && sed -n '1,220p' SECURITY.md && sed -n '1,220p' SUPPORT.md`

### Step 4

**Files:** `GOVERNANCE.md`, `.github/CODEOWNERS`

**Change:** Add the lightweight governance model and broad ownership mapping for major repo areas and policy files.

**Verify:** `sed -n '1,220p' GOVERNANCE.md && sed -n '1,220p' .github/CODEOWNERS`

### Step 5

**Files:** `README.md`, `docs/README.md`

**Change:** Update the root README and docs index so contributors can find the new policy set quickly from the main project entry points.

**Verify:** `rg -n "Contributing|Code of Conduct|Security|Support|Governance" README.md docs/README.md`

### Step 6

**Files:** `CHANGELOG.md`

**Change:** Record the new OSS governance policy set under `Unreleased` in `CHANGELOG.md`.

**Verify:** `rg -n "Governance|CONTRIBUTING|CODE_OF_CONDUCT|SECURITY|SUPPORT|GOVERNANCE" CHANGELOG.md`

### Step 7

**Files:** `CONTRIBUTING.md`, `CODE_OF_CONDUCT.md`, `SECURITY.md`, `SUPPORT.md`, `GOVERNANCE.md`, `.github/CODEOWNERS`, `README.md`, `docs/README.md`, `CHANGELOG.md`

**Change:** Run final verification for file presence, policy discoverability, and repository sanity after the docs/meta change.

**Verify:** `test -f CONTRIBUTING.md -a -f CODE_OF_CONDUCT.md -a -f SECURITY.md -a -f SUPPORT.md -a -f GOVERNANCE.md -a -f .github/CODEOWNERS && pnpm --filter @hive/web build`

## Risks & mitigations

- Risk: policy docs drift from actual practice.
  - Mitigation: derive commands and expectations directly from `AGENTS.md`, `README.md`, and existing docs.
- Risk: governance language implies a larger maintainer body than exists.
  - Mitigation: use role-based governance and future-friendly wording instead of naming a committee.
- Risk: contributor-facing docs become hard to discover.
  - Mitigation: link the policy set from both `README.md` and `docs/README.md`.

## Rollback plan

- Revert the new root policy files and `CODEOWNERS` if maintainers want a narrower OSS scope.
- Revert the `README.md`, `docs/README.md`, and `CHANGELOG.md` link additions if the doc structure is revised.
- Because there are no runtime changes, rollback is a clean docs/meta revert with no data or migration impact.
