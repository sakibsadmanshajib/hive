# Sprint 1 Plan

**Dates:** 2026-06-12 to 2026-06-26 (2 weeks)
**Milestone:** v1.1 (target 2026-06-30)
**Owner:** sakibsadmanshajib

---

## Sprint Goal

Close the remaining v1.1 ship-gate gaps so the milestone is packaged and ready for launch by 2026-06-30. Specifically: complete Phase 20 wave 4 (provider catalog verification), harden the auth and payment surfaces (bKash adapter, free-tier abuse guard, user settings), ship the Langfuse observability runbook, and resolve the two open architectural decision records (OpenAI auth canonical shape, chat runtime separation decision). Every item in this sprint must exit through the Needs Testing column before the sprint ends.

---

## Committed Backlog

| # | Issue | Title | Size | Acceptance Criteria |
|---|-------|-------|------|---------------------|
| 1 | [#68](https://github.com/sakibsadmanshajib/hive/issues/68) | OpenRouter metadata capture for token and cost tracking | S | Token counts and cost fields populated in usage records; unit test covers the mapping; no provider name leaks in response. |
| 2 | [#50](https://github.com/sakibsadmanshajib/hive/issues/50) | Implement /v1/users/settings for the shipped web settings panel | M | GET and PATCH /v1/users/settings return/persist user prefs; web-console panel reads and writes correctly; integration test green. |
| 3 | [#67](https://github.com/sakibsadmanshajib/hive/issues/67) | bKash payment gateway adapter | L | bKash checkout flow initiates and completes in staging; payment record created with correct BDT amount; no USD/FX language in any response surface; regulatory lint passes. |
| 4 | [#71](https://github.com/sakibsadmanshajib/hive/issues/71) | Restrict anonymous chat to authenticated users only | S | Unauthenticated POST to chat endpoint returns 401; existing auth tests still green; cross-origin guest-session test updated. |
| 5 | [#116](https://github.com/sakibsadmanshajib/hive/issues/116) | Free-tier abuse: CAPTCHA, IP signup rate limit, email verification (P0) | L | Signup rate limit enforced at edge; email verification gate active; at least one anti-abuse layer (CAPTCHA or IP throttle) deployed; test covering rate-limit reject path. |
| 6 | [#65](https://github.com/sakibsadmanshajib/hive/issues/65) | Create chat-history rollout runbook with feature flag and rollback | S | Runbook doc committed to .planning/; feature flag name documented; rollback steps verified manually in staging. |
| 7 | [#70](https://github.com/sakibsadmanshajib/hive/issues/70) | Langfuse observability integration verification and setup guide | S | Setup guide committed; Langfuse trace visible for at least one real inference call in staging; guide reviewed and merged. |
| 8 | [#55](https://github.com/sakibsadmanshajib/hive/issues/55) | Decide and document canonical OpenAI auth compatibility for inference | S | ADR committed to .planning/; decision covers Bearer token shape, key prefix convention, and SDK compatibility notes. |
| 9 | [#66](https://github.com/sakibsadmanshajib/hive/issues/66) | Align OpenAPI spec with implemented chat history endpoints | S | openapi.yaml updated to match all shipped chat-history routes; no undocumented endpoints remain for v1.1 surface. |
| 10 | [#64](https://github.com/sakibsadmanshajib/hive/issues/64) | Add cross-origin rejection test for guest-session proxy | S | Test file added; test fails on a permissive CORS config and passes on the correct restrictive config; CI green. |
| 11 | [#54](https://github.com/sakibsadmanshajib/hive/issues/54) | Close remaining /v1/images/generations OpenAI schema gaps | M | All required OpenAI request/response fields present; contract test in openai-contract package updated; no regression on existing image generation path. |
| 12 | Phase 20 wave 4 | Provider catalog verification pass (Phase 20 completion) | M | Wave 4 checklist from .planning/phases/20-provider-catalog/ fully green; LiteLLM sync tested end-to-end; custom provider CRUD exercised in staging; phase marked complete in STATE.md. |

**Total sizing:** 4 S + 5 M + 3 L (roughly 10 story-point equivalent per size tier: S=1, M=2, L=3 = 23 points)

---

## Definition of Done

An item is Done when ALL of the following are true:

1. Code merged to main (PR approved, all required checks green, zero unresolved review threads).
2. Unit or integration test covering the new behaviour exists and passes in CI.
3. No new `console.log` in production code paths.
4. No hardcoded secrets or credentials.
5. Provider names do not appear in any customer-visible API response.
6. For payment-touching changes: regulatory lint (`lint-no-customer-usd.mjs`) passes manually.
7. Relevant docs or runbooks updated (openapi.yaml, .planning/ entries, or inline comments as appropriate).

---

## Needs Testing Exit Criteria

An item moves from In Review to Needs Testing when the PR is merged. It exits Needs Testing (moves to Done) when:

1. A staging smoke test or manual verification step confirms the feature works on the live staging environment.
2. The verifier records evidence (screenshot, curl output, log line) as a comment on the issue or in .planning/phases/.
3. For payment flows: a test transaction (real or sandbox) completes without error and without exposing USD/FX data.
4. For security hardening items: the attack path is manually exercised and blocked.

---

## Sprint 2 Stub

**Dates:** 2026-06-27 to 2026-07-10
**Theme:** Provider intelligence, chat runtime separation, rate-limit architecture

Candidate items (to be groomed before sprint start):

- [#57](https://github.com/sakibsadmanshajib/hive/issues/57) Separate authenticated web chat runtime from the public OpenAI-compatible surface (M)
- [#74](https://github.com/sakibsadmanshajib/hive/issues/74) Separate rate limits and analytics for API vs Web tiers (M)
- [#37](https://github.com/sakibsadmanshajib/hive/issues/37) Build OpenRouter model intelligence collector (L)
- [#45](https://github.com/sakibsadmanshajib/hive/issues/45) Add normalized provider and model catalog with provenance (M)
- [#63](https://github.com/sakibsadmanshajib/hive/issues/63) Add try/catch error handling to guest-session proxy fetch (S)
- [#84](https://github.com/sakibsadmanshajib/hive/issues/84) Implement /v1/batches (OpenAI compatibility) (L)
- Phase 25 chat-app re-audit (ship-gate: chat_app_reaudit)

Sprint 2 goal: clear the remaining v1.1 architectural decisions and ship the provider intelligence layer, leaving only packaging and final staging audit for the v1.1 cutover window.

---

## Board Reference

Project: https://github.com/users/sakibsadmanshajib/projects/3
Field IDs (for `gh project item-edit`):

| Field | ID |
|-------|----|
| Status | `PVTSSF_lAHOAjF1kc4BaVEzzhVNdgw` |
| Sprint | `PVTSSF_lAHOAjF1kc4BaVEzzhVTSds` |
| Horizon | `PVTSSF_lAHOAjF1kc4BaVEzzhVTKdI` |
| Target | `PVTF_lAHOAjF1kc4BaVEzzhVTKdM` |

Sprint option IDs: Sprint 1 `81d236d4`, Sprint 2 `ac10a8f4`, Sprint 3 `41489897`, Backlog `d94bfc74`
Status option IDs: Backlog `734832d4`, Ready `3ad263f8`, In Progress `701ea7a7`, In Review `4b9bebf0`, Needs Testing `98a0d9ec`, Done `653759a9`, Blocked `c5a5d52b`
