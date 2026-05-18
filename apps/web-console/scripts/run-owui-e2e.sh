#!/usr/bin/env bash
# Cost-aware wrapper for the OWUI Playwright suite. Spend itself is capped
# by the LiteLLM-configured per-key limit; this script only records wall
# time and prints a summary line. Phase 21 introduces the hard runtime
# kill-switch via the credits engine.

set -euo pipefail

# Resolve apps/web-console regardless of caller CWD so the embedded
# --config path stays correct.
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
APP_DIR="$(cd "${SCRIPT_DIR}/.." && pwd)"
cd "$APP_DIR"

START=$(date -u +%s)
finish() {
  END=$(date -u +%s)
  echo "owui_e2e_run_seconds=$((END - START))"
}
trap finish EXIT

pnpm exec playwright test \
  --config=e2e/phase-19/owui/playwright.owui.config.ts \
  --project=owui
