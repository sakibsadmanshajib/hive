# GitHub Triage Report

**Date**: 2026-02-28

This document captures the triage status of stale and conflicting GitHub issues against merged pull requests. 

## Triaged Issues

| Issue # | Title | Current Status | Triaged Status | Evidence / Notes |
|---|---|---|---|---|
| **#16** | Add web e2e smoke coverage for auth -> chat -> billing flow | OPEN | **Delivered / To Close** | Delivered via merged PR **#21**: `test(web): add Playwright smoke coverage for auth-chat-billing flow` |
| **#11** | Add CI PR quality gates for API and web builds/tests | OPEN | **Superseded / To Close** | Delivered via merged PR **#18**: `feat(ops): add cost-optimized CI quality gates and PR cleanup` (tracked under closed issue #17) |
| **#31** | Feature: Add OpenRouter AI Provider Integration - Phase 1 Foundation | OPEN | **Blocked / Reverted** | PRs **#32**, **#33**, and **#34** explicitly remove OpenRouter tests and artifacts. Issue #31 is blocked/superseded and needs re-evaluation. |
| **#37** | Build OpenRouter model intelligence collector | OPEN | **Blocked / Reverted** | Depends on #31 which is blocked. |
| **#14** | Critical web flow gaps block end-to-end auth→chat→billing journey | CLOSED | **Delivered** | Resolved correctly by PR **#15**: `feat(web): move to guarded chat-first home and modern workspace flow` |
| **#17** | Harden monorepo CI quality gates and post-merge cleanup | CLOSED | **Delivered** | Resolved correctly by PR **#18**: `feat(ops): add cost-optimized CI quality gates and PR cleanup` |
| **#28** | Optimize smoke CI workflow with cache/artifact reuse | CLOSED | **Delivered** | Resolved correctly by PR **#29**: `chore(ci): optimize smoke workflow cache reuse` |
| **#19** | Add zero-cost free tier with paid-only API-key inference access | OPEN | **Still Needed / Active** | PR **#20** added planning artifacts, but implementation is pending. |

## Actions Executed
- Addressed stale issues that were already implemented but left open.
- Identified abandoned/reverted feature tracks (OpenRouter) to prevent conflicting development.
