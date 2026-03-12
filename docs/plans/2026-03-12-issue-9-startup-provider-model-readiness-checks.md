## Goal

Implement issue `#9` by adding zero-token startup provider model readiness checks, persisting readiness results for internal status, and documenting the operator-facing behavior without making API startup fail when a provider model is unavailable.

## Assumptions

- The preferred plan-writer helper at `.agent/skills/superpowers-workflow/scripts/write_artifact.py` is unavailable in this repository, so this plan is written directly to `docs/plans/`.
- Readiness verification must not spend provider chat tokens, so checks need to reuse provider metadata endpoints rather than `chat()` requests.
- Existing public provider status and metrics endpoints must remain sanitized; only internal status should expose readiness detail.
- The work is API-only unless documentation updates require broader repo verification.

## Plan

### Step 1

**Files:** `apps/api/src/providers/types.ts`, `apps/api/src/providers/ollama-client.ts`, `apps/api/src/providers/groq-client.ts`, `apps/api/src/providers/mock-client.ts`

**Change:** Add an explicit provider readiness contract for zero-token configured-model verification and inspect the current adapter implementations to confirm which metadata endpoints each provider can use without request-token spend.

**Verify:** `sed -n '1,220p' apps/api/src/providers/types.ts && sed -n '1,220p' apps/api/src/providers/ollama-client.ts && sed -n '1,220p' apps/api/src/providers/groq-client.ts && sed -n '1,160p' apps/api/src/providers/mock-client.ts`

### Step 2

**Files:** `apps/api/test/providers/provider-registry.test.ts`, `apps/api/test/providers/provider-status.test.ts`, `apps/api/test/routes/providers-status-route.test.ts`

**Change:** Add failing tests for persisted startup readiness snapshots, internal detail enrichment, and unchanged public-status sanitization before changing runtime code.

**Verify:** `pnpm --filter @hive/api exec vitest run apps/api/test/providers/provider-registry.test.ts apps/api/test/providers/provider-status.test.ts apps/api/test/routes/providers-status-route.test.ts`

### Step 3

**Files:** `apps/api/test/providers/ollama-client.test.ts`, `apps/api/test/providers/groq-client.test.ts`

**Change:** Add failing adapter-level tests that prove Ollama and Groq can validate configured model availability from metadata endpoints alone and classify missing-model versus unreachable cases.

**Verify:** `pnpm --filter @hive/api exec vitest run apps/api/test/providers/ollama-client.test.ts apps/api/test/providers/groq-client.test.ts`

### Step 4

**Files:** `apps/api/src/providers/types.ts`, `apps/api/src/providers/ollama-client.ts`, `apps/api/src/providers/groq-client.ts`, `apps/api/src/providers/mock-client.ts`

**Change:** Implement the zero-token readiness method on each provider client, including metadata parsing and clear readiness-detail messages for disabled, unreachable, ready, and missing-model outcomes.

**Verify:** `pnpm --filter @hive/api exec vitest run apps/api/test/providers/ollama-client.test.ts apps/api/test/providers/groq-client.test.ts`

### Step 5

**Files:** `apps/api/src/providers/registry.ts`, `apps/api/test/providers/provider-registry.test.ts`, `apps/api/test/providers/provider-status.test.ts`

**Change:** Extend the provider registry to store startup readiness snapshots separately from circuit and request metrics, expose enriched internal status detail, and keep public sanitization behavior unchanged.

**Verify:** `pnpm --filter @hive/api exec vitest run apps/api/test/providers/provider-registry.test.ts apps/api/test/providers/provider-status.test.ts apps/api/test/routes/providers-status-route.test.ts`

### Step 6

**Files:** `apps/api/src/runtime/services.ts`, `apps/api/test/runtime/services.test.ts`

**Change:** Run the readiness sweep during runtime service construction, log warnings for enabled-but-unready providers, and confirm startup stays available instead of throwing.

**Verify:** `pnpm --filter @hive/api exec vitest run apps/api/test/runtime/services.test.ts`

### Step 7

**Files:** `docs/runbooks/active/provider-circuit-breaker.md`, `README.md`, `docs/README.md`, `CHANGELOG.md`

**Change:** Document the new startup readiness behavior, explain that checks are zero-token and startup is degraded rather than blocked, and update the main discovery docs plus changelog.

**Verify:** `rg -n "startup|readiness|model ready|provider status" docs/runbooks/active/provider-circuit-breaker.md README.md docs/README.md CHANGELOG.md`

### Step 8

**Files:** `apps/api/src/providers/types.ts`, `apps/api/src/providers/ollama-client.ts`, `apps/api/src/providers/groq-client.ts`, `apps/api/src/providers/mock-client.ts`, `apps/api/src/providers/registry.ts`, `apps/api/src/runtime/services.ts`, `apps/api/test/providers/provider-registry.test.ts`, `apps/api/test/providers/provider-status.test.ts`, `apps/api/test/routes/providers-status-route.test.ts`, `apps/api/test/providers/ollama-client.test.ts`, `apps/api/test/providers/groq-client.test.ts`, `apps/api/test/runtime/services.test.ts`, `docs/runbooks/active/provider-circuit-breaker.md`, `README.md`, `docs/README.md`, `CHANGELOG.md`

**Change:** Run final verification for the touched API and docs scope, capture the exact commands used as evidence, and paste the final command/results block into the PR checklist description or a follow-up PR comment so reviewers can verify the execution evidence in one consistent location.

**Verify:** `pnpm --filter @hive/api test && pnpm --filter @hive/api build`

## Risks & mitigations

- Risk: startup readiness logic accidentally spends provider tokens.
  Mitigation: constrain readiness checks to metadata endpoints only and add adapter tests that never call `chat()`.
- Risk: internal readiness detail leaks through the public status surface.
  Mitigation: keep the existing route sanitization tests and add explicit assertions that `detail` remains absent from the public endpoint.
- Risk: startup warnings become noisy or misleading for disabled providers.
  Mitigation: classify disabled providers separately and skip warnings when configuration already marks them unavailable.
- Risk: readiness state gets conflated with live circuit-breaker or health data.
  Mitigation: store startup readiness as a separate snapshot in the registry and avoid mutating request metrics or circuit state during startup checks.

## Rollback plan

- Revert the provider readiness interface, registry snapshot logic, runtime startup sweep, and associated tests/docs in one change if the behavior proves too noisy or confusing.
- If only the internal-status enrichment is problematic, revert that surface while keeping adapter readiness helpers private until a revised design is ready.
- Because this change should not alter persisted application data or public API schemas, rollback is limited to runtime behavior and documentation.
