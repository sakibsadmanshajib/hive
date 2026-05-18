#!/usr/bin/env bash
# Cost-aware wrapper for the OWUI Playwright suite. Spend itself is capped
# by the LiteLLM-configured per-key limit; this script only records wall
# time and prints a summary line. Phase 21 introduces the hard runtime
# kill-switch via the credits engine.

set -euo pipefail

START=$(date -u +%s)
pnpm exec playwright test \
  --config=e2e/phase-19/owui/playwright.owui.config.ts \
  --project=owui
END=$(date -u +%s)
echo "owui_e2e_run_seconds=$((END - START))"
