---
phase: 1
slug: contract-compatibility-harness
status: approved
nyquist_compliant: true
wave_0_complete: true
created: 2026-03-28
updated: 2026-03-29
verified: 2026-03-29
---

# Phase 1 — Validation Strategy

> Retroactive Nyquist audit completed after plans `01-01` through `01-04` and final phase re-verification.

---

## Test Infrastructure

| Property | Value |
|----------|-------|
| **Framework (Go runtime/contracts)** | `go test` via Docker toolchain |
| **Framework (JS SDK)** | Vitest `3.2.4` |
| **Framework (Python SDK)** | pytest `9.0.2` |
| **Framework (Java SDK)** | JUnit 5 via Gradle `8.14.4` |
| **Framework (Contract generator)** | `python3 -m unittest` |
| **Config files** | `packages/sdk-tests/js/vitest.config.ts`, `packages/sdk-tests/python/pyproject.toml`, `packages/sdk-tests/java/build.gradle` |
| **Quick run command** | `docker compose -f deploy/docker/docker-compose.yml run --rm -T toolchain sh -lc 'cd /workspace/apps/edge-api && go test ./internal/errors/... ./internal/matrix/... ./internal/middleware/... ./docs/... ./cmd/server/... -count=1'` |
| **Full suite command** | `docker compose -f deploy/docker/docker-compose.yml up -d edge-api && docker compose -f deploy/docker/docker-compose.yml run --rm -T sdk-tests-js && docker compose -f deploy/docker/docker-compose.yml run --rm -T sdk-tests-py && docker compose -f deploy/docker/docker-compose.yml run --rm -T sdk-tests-java && docker compose -f deploy/docker/docker-compose.yml run --rm -T toolchain sh -lc 'cd /workspace && packages/openai-contract/scripts/generate-matrix.sh' && docker compose -f deploy/docker/docker-compose.yml run --rm -T toolchain sh -lc 'cd /workspace/apps/edge-api && go test ./internal/errors/... ./internal/matrix/... ./internal/middleware/... ./docs/... ./cmd/server/... -count=1' && docker compose -f deploy/docker/docker-compose.yml down` |
| **Estimated runtime** | ~60 seconds |

---

## Sampling Rate

- **After every task commit:** Run the quick Go contract/docs package suite in the toolchain container.
- **After every plan wave:** Run the full SDK harness, generator sync, and Go contract/docs package suite.
- **Before `$gsd-verify-work`:** Full suite must be green.
- **Max feedback latency:** 120 seconds.

---

## Per-Task Verification Map

| Task ID | Plan | Wave | Requirement | Test Type | Automated Command | File Exists | Status |
|---------|------|------|-------------|-----------|-------------------|-------------|--------|
| 01-01-01 | 01 | 1 | API-08 | smoke | `cd /home/sakib/hive && test -f go.work && test -f apps/edge-api/go.mod && test -f apps/edge-api/cmd/server/main.go && test -f apps/edge-api/.air.toml && test -f .gitignore && echo PASS` | ✅ | ✅ green |
| 01-01-02 | 01 | 1 | API-08 | smoke | `docker compose -f deploy/docker/docker-compose.yml config --quiet` | ✅ | ✅ green |
| 01-01-03 | 01 | 1 | API-08 | integration | `docker compose -f deploy/docker/docker-compose.yml build edge-api && docker compose -f deploy/docker/docker-compose.yml up -d edge-api && curl -sf http://localhost:8080/health && curl -sf http://localhost:8080/v1/models && docker compose -f deploy/docker/docker-compose.yml down` | ✅ | ✅ green |
| 01-02-01 | 02 | 2 | COMP-02, API-08 | unit | `cd /workspace/apps/edge-api && go test ./internal/errors/... ./internal/matrix/... -count=1` | ✅ | ✅ green |
| 01-02-02 | 02 | 2 | COMP-02, COMP-03, API-08 | unit | `cd /workspace/apps/edge-api && go test ./internal/middleware/... -count=1 && go build ./cmd/server` | ✅ | ✅ green |
| 01-03-01 | 03 | 3 | COMP-01, COMP-02 | integration | `docker compose -f deploy/docker/docker-compose.yml run --rm -T sdk-tests-js` | ✅ | ✅ green |
| 01-03-02 | 03 | 3 | COMP-01, COMP-02 | integration | `docker compose -f deploy/docker/docker-compose.yml run --rm -T sdk-tests-py && docker compose -f deploy/docker/docker-compose.yml run --rm -T sdk-tests-java` | ✅ | ✅ green |
| 01-03-03 | 03 | 3 | COMP-01, COMP-02, COMP-03, API-08 | end-to-end | `docker compose -f deploy/docker/docker-compose.yml up -d edge-api && docker compose -f deploy/docker/docker-compose.yml run --rm sdk-tests-js && docker compose -f deploy/docker/docker-compose.yml run --rm sdk-tests-py && docker compose -f deploy/docker/docker-compose.yml run --rm sdk-tests-java && docker compose -f deploy/docker/docker-compose.yml run --rm edge-api go test ./... -v && curl -sf http://localhost:8080/docs/ | grep -q swagger-ui && docker compose -f deploy/docker/docker-compose.yml down` | ✅ | ✅ green |
| 01-04-01 | 04 | 4 | COMP-03 | unit/integration | `python3 -m unittest packages.openai-contract.scripts.test_sync_hive_contract && docker compose -f deploy/docker/docker-compose.yml run --rm -T toolchain sh -lc 'cd /workspace && packages/openai-contract/scripts/generate-matrix.sh'` | ✅ | ✅ green |
| 01-04-02 | 04 | 4 | COMP-03 | unit | `docker compose -f deploy/docker/docker-compose.yml run --rm -T toolchain sh -lc 'cd /workspace/apps/edge-api && go test ./docs/... ./cmd/server/... -count=1'` | ✅ | ✅ green |

*Status: ⬜ pending · ✅ green · ❌ red · ⚠️ flaky*

---

## Wave 0 Requirements

Existing infrastructure covers all phase requirements. No deferred Wave 0 validation scaffolding remains.

---

## Manual-Only Verifications

| Behavior | Requirement | Why Manual | Test Instructions |
|----------|-------------|------------|-------------------|
| Hot-reload after editing a Go file in the running dev container | API-08 supporting developer workflow | File-watch timing depends on local Docker sync behavior and is not part of the launch-surface compatibility contract. Automated coverage already proves the container boots, rebuilds, and serves the contract surface. | Run `docker compose -f deploy/docker/docker-compose.yml up edge-api`, edit `apps/edge-api/cmd/server/main.go`, then confirm a rebuild in `docker compose logs -f edge-api`. |

---

## Validation Sign-Off

- [x] All phase requirements have automated verification.
- [x] Sampling continuity: no 3 consecutive tasks without automated verify.
- [x] No Wave 0 dependencies remain.
- [x] No watch-mode flags in quick or full validation commands.
- [x] Feedback latency is within 120 seconds for the current suite.
- [x] `nyquist_compliant: true` set in frontmatter.

**Approval:** approved 2026-03-29

---

## Validation Audit 2026-03-29

| Metric | Count |
|--------|-------|
| Gaps found | 0 |
| Resolved | 0 |
| Escalated | 0 |

Evidence used for this audit:

- `python3 -m unittest packages.openai-contract.scripts.test_sync_hive_contract` passed: 3 tests.
- `docker compose -f deploy/docker/docker-compose.yml run --rm -T sdk-tests-js` passed: 6 files, 11 tests.
- `docker compose -f deploy/docker/docker-compose.yml run --rm -T sdk-tests-py` passed: 9 tests.
- `docker compose -f deploy/docker/docker-compose.yml run --rm -T sdk-tests-java` passed: Gradle `BUILD SUCCESSFUL`.
- `docker compose -f deploy/docker/docker-compose.yml run --rm -T toolchain sh -lc 'cd /workspace && packages/openai-contract/scripts/generate-matrix.sh'` exited `0`.
- `docker compose -f deploy/docker/docker-compose.yml run --rm -T toolchain sh -lc 'cd /workspace/apps/edge-api && go test ./internal/errors/... ./internal/matrix/... ./internal/middleware/... ./docs/... ./cmd/server/... -count=1'` exited `0`.
- `.planning/phases/01-contract-compatibility-harness/01-VERIFICATION.md` already recorded phase verification as passed with `7/7` must-haves on `2026-03-29`.
