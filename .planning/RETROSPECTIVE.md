# Project Retrospective

*A living document updated after each milestone. Lessons feed forward into future planning.*

## Milestone: v1.0 - OpenAI API Compliance

**Shipped:** 2026-03-22
**Phases:** 13 | **Plans:** 25 | **Sessions:** n/a

### What Was Built
- A scoped `/v1/*` API surface that now behaves like OpenAI across errors, auth, models, chat completions, embeddings, images, and responses
- A DIFF-header contract that holds on success paths and on early-return auth, validation, not-found, and stub paths
- A CI-ready real OpenAI SDK regression suite plus Docker-local verification evidence for the shipped milestone

### What Worked
- Small phase plans with explicit verification commands kept milestone progress fast and reviewable
- Combining route-level regressions with real SDK checks caught contract issues that schema fixtures alone would have missed
- Docker-only build verification kept the public API evidence aligned with the actual runtime environment

### What Was Inefficient
- Phase 12 needed artifact recovery after execution, which added avoidable cleanup work late in the milestone
- Out-of-order phase closure required manual roadmap/state verification after the tooling said the milestone was complete
- The milestone summary CLI relied on optional `one_liner` fields that older summaries did not populate, so manual accomplishment extraction was still necessary

### Patterns Established
- Keep OpenAI contract behavior behind the scoped `v1Plugin` and shared `/v1/*` helpers
- Prefer real SDK regression paths for milestone acceptance, especially for auth, streaming, and alias handling
- Preserve no-dispatch DIFF headers before shared helpers can terminate a request

### Key Lessons
1. When phases execute out of numeric order, manually verify `.planning/ROADMAP.md` and `.planning/STATE.md` before trusting milestone-complete output.
2. Embeddings alias coverage must hit the real runtime path; helper-only catalogs can hide production mismatches.
3. Shared reply-header helpers in this repo must not assume chainable `reply.header()` mocks.

### Cost Observations
- Model mix: n/a
- Sessions: n/a
- Notable: Most late-stage overhead came from audit closure and artifact recovery rather than new runtime implementation

---

## Cross-Milestone Trends

### Process Evolution

| Milestone | Sessions | Phases | Key Change |
|-----------|----------|--------|------------|
| v1.0 | n/a | 13 | Shifted milestone acceptance toward real-SDK regressions plus Docker-local build evidence |

### Cumulative Quality

| Milestone | Tests | Coverage | Zero-Dep Additions |
|-----------|-------|----------|-------------------|
| v1.0 | 372 API tests at final audit | n/a | Maintained zero new runtime dependencies for milestone-closing fixes |

### Top Lessons (Verified Across Milestones)

1. Route-level fixtures are not enough for API-contract work; real-client regressions need to backstop milestone closure.
2. Planning artifacts need the same discipline as code artifacts, or milestone closeout becomes slower than implementation.
