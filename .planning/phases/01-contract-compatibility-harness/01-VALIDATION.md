---
phase: 1
slug: contract-compatibility-harness
status: draft
nyquist_compliant: false
wave_0_complete: false
created: 2026-03-28
---

# Phase 1 — Validation Strategy

> Per-phase validation contract for feedback sampling during execution.

---

## Test Infrastructure

| Property | Value |
|----------|-------|
| **Framework (Go)** | `go test` with standard library |
| **Framework (JS SDK)** | Vitest 3.x |
| **Framework (Python SDK)** | pytest 8.x |
| **Framework (Java SDK)** | JUnit 5 + Gradle |
| **Config file** | None yet — Wave 0 installs |
| **Quick run command** | `docker compose run --rm edge-api go test ./... -short` |
| **Full suite command** | `docker compose run --rm sdk-tests-js npm test && docker compose run --rm sdk-tests-py pytest && docker compose run --rm sdk-tests-java ./gradlew test && docker compose run --rm edge-api go test ./...` |
| **Estimated runtime** | ~120 seconds |

---

## Sampling Rate

- **After every task commit:** Run `docker compose run --rm edge-api go test ./... -short`
- **After every plan wave:** Run full suite across all SDK languages + Go unit tests
- **Before `/gsd:verify-work`:** Full suite must be green
- **Max feedback latency:** 120 seconds

---

## Per-Task Verification Map

| Task ID | Plan | Wave | Requirement | Test Type | Automated Command | File Exists | Status |
|---------|------|------|-------------|-----------|-------------------|-------------|--------|
| 01-01-01 | 01 | 1 | API-08 | smoke | `docker compose up -d && docker compose ps --filter status=running` | ❌ W0 | ⬜ pending |
| 01-01-02 | 01 | 1 | API-08 | smoke | `docker compose run --rm edge-api go test ./... -short` | ❌ W0 | ⬜ pending |
| 01-02-01 | 02 | 2 | COMP-01 | integration | `docker compose run --rm sdk-tests-js npm test` | ❌ W0 | ⬜ pending |
| 01-02-02 | 02 | 2 | COMP-01 | integration | `docker compose run --rm sdk-tests-py pytest` | ❌ W0 | ⬜ pending |
| 01-02-03 | 02 | 2 | COMP-01 | integration | `docker compose run --rm sdk-tests-java ./gradlew test` | ❌ W0 | ⬜ pending |
| 01-02-04 | 02 | 2 | COMP-02 | unit | `docker compose run --rm edge-api go test ./internal/errors/... -run TestOpenAIError` | ❌ W0 | ⬜ pending |
| 01-02-05 | 02 | 2 | COMP-02 | integration | `docker compose run --rm sdk-tests-js npm test -- --grep "unsupported"` | ❌ W0 | ⬜ pending |
| 01-02-06 | 02 | 2 | COMP-02 | integration | `docker compose run --rm sdk-tests-js npm test -- --grep "headers"` | ❌ W0 | ⬜ pending |
| 01-03-01 | 03 | 2 | COMP-03 | smoke | `curl -sf http://localhost:8080/docs/ \| grep -q swagger-ui` | ❌ W0 | ⬜ pending |
| 01-03-02 | 03 | 2 | API-08 | integration | `docker compose run --rm sdk-tests-js npm test -- --grep "unsupported"` | ❌ W0 | ⬜ pending |

*Status: ⬜ pending · ✅ green · ❌ red · ⚠️ flaky*

---

## Wave 0 Requirements

- [ ] `deploy/docker/docker-compose.yml` — Docker Compose orchestration for all services
- [ ] `deploy/docker/Dockerfile.edge-api` — Go dev image with air
- [ ] `deploy/docker/Dockerfile.toolchain` — Codegen tools container
- [ ] `deploy/docker/Dockerfile.sdk-tests-js` — Node + OpenAI SDK + Vitest
- [ ] `deploy/docker/Dockerfile.sdk-tests-py` — Python + OpenAI SDK + pytest
- [ ] `deploy/docker/Dockerfile.sdk-tests-java` — Java + OpenAI SDK + JUnit/Gradle
- [ ] `apps/edge-api/go.mod` — Go module initialization
- [ ] `packages/sdk-tests/js/package.json` — JS test project with vitest + openai SDK
- [ ] `packages/sdk-tests/python/pyproject.toml` — Python test project with pytest + openai SDK
- [ ] `packages/sdk-tests/java/build.gradle` — Java test project with JUnit + openai SDK
- [ ] `packages/openai-contract/upstream/openapi.yaml` — Imported spec (pinned SHA)

---

## Manual-Only Verifications

| Behavior | Requirement | Why Manual | Test Instructions |
|----------|-------------|------------|-------------------|
| Swagger UI renders correctly in browser | COMP-03 | Visual rendering quality | Open `http://localhost:8080/docs/` in browser, verify spec loads and endpoints are browsable |
| Hot-reload works on file save | API-08 | Requires file-system event + container restart observation | Edit a Go file, save, verify container reloads within 5s via `docker compose logs -f edge-api` |

---

## Validation Sign-Off

- [ ] All tasks have `<automated>` verify or Wave 0 dependencies
- [ ] Sampling continuity: no 3 consecutive tasks without automated verify
- [ ] Wave 0 covers all MISSING references
- [ ] No watch-mode flags
- [ ] Feedback latency < 120s
- [ ] `nyquist_compliant: true` set in frontmatter

**Approval:** pending
