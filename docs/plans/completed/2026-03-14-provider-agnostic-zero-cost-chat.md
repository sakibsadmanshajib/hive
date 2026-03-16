## Goal

Fix the `guest-free` regressions and implement a provider-agnostic zero-cost chat routing layer that keeps the public API OpenAI-compatible while making OpenRouter, OpenAI, Groq, Gemini, and Anthropic ready for chat.

## Assumptions

- The public API contract for `/v1/models`, `/v1/chat/completions`, `/v1/responses`, and `/v1/images/generations` remains OpenAI-compatible.
- Public model ids such as `guest-free`, `fast-chat`, and `smart-reasoning` remain stable product-facing ids even if the backing provider offers change.
- The initial provider-offer catalog stays code-defined and environment-backed rather than database-driven.
- Anthropic uses a native adapter by default because its OpenAI compatibility layer is not the preferred production path.
- Zero-cost traffic must fail closed when no healthy zero-cost provider offer is available; it must not fall through to paid providers.

## Plan

1. Files: `apps/api/test/domain/runtime-chat-billing.test.ts`, `apps/api/test/routes/guest-chat-route.test.ts`
   Change: Add failing tests that prove authenticated `guest-free` requests bypass credit consumption and that guest `guest-free` no longer depends on the mock echo path.
   Verify: `pnpm --filter @hive/api exec vitest run test/domain/runtime-chat-billing.test.ts test/routes/guest-chat-route.test.ts`

2. Files: `apps/api/test/providers/provider-registry.test.ts`, `apps/api/test/providers/provider-status.test.ts`
   Change: Add failing provider-registry tests for zero-cost-only offer selection, no paid fallback for zero-cost routes, and readiness/status coverage for the expanded provider set.
   Verify: `pnpm --filter @hive/api exec vitest run test/providers/provider-registry.test.ts test/providers/provider-status.test.ts`

3. Files: `apps/api/test/domain/env.test.ts`, `apps/api/test/providers/openai-client.test.ts`, new test files under `apps/api/test/providers/`
   Change: Add failing tests for new provider env parsing plus adapter behavior for OpenAI-compatible chat transport, Gemini/OpenRouter/Groq configuration, and Anthropic native translation.
   Verify: `pnpm --filter @hive/api exec vitest run test/domain/env.test.ts test/providers/openai-client.test.ts`

4. Files: `apps/api/src/config/env.ts`, `.env.example`
   Change: Add provider configuration for OpenRouter, OpenAI chat, Gemini, and Anthropic while preserving existing OpenAI image configuration and current provider timeout/retry conventions.
   Verify: `pnpm --filter @hive/api exec vitest run test/domain/env.test.ts`

5. Files: new files under `apps/api/src/providers/`, `apps/api/src/providers/types.ts`
   Change: Introduce a shared OpenAI-compatible chat transport for OpenRouter/OpenAI/Groq/Gemini and a native Anthropic chat adapter behind the existing provider interface.
   Verify: `pnpm --filter @hive/api exec vitest run test/providers/openai-client.test.ts`

6. Files: new catalog files under `apps/api/src/providers/` or `apps/api/src/domain/`, `apps/api/src/domain/model-service.ts`, `apps/api/src/domain/types.ts`
   Change: Split public virtual models from internal provider offers, add zero-cost offer metadata, and map `guest-free` to zero-cost eligible offers without exposing provider internals publicly.
   Verify: `pnpm --filter @hive/api exec vitest run test/domain/model-service.test.ts test/providers/provider-registry.test.ts`

7. Files: `apps/api/src/providers/registry.ts`, `apps/api/src/runtime/services.ts`
   Change: Update routing to choose eligible offers per public model, bypass credit consumption for zero-cost public models, forbid paid fallback for zero-cost requests, and preserve existing refund behavior for paid models.
   Verify: `pnpm --filter @hive/api exec vitest run test/domain/runtime-chat-billing.test.ts test/providers/provider-registry.test.ts test/routes/chat-completions-route.test.ts`

8. Files: `apps/api/src/runtime/services.ts`, `apps/api/src/routes/guest-chat.ts`, `apps/api/src/routes/chat-completions.ts`
   Change: Ensure guest and authenticated chat flows both use the new provider-backed zero-cost path for `guest-free` while keeping request and response payloads OpenAI-compatible.
   Verify: `pnpm --filter @hive/api exec vitest run test/routes/guest-chat-route.test.ts test/routes/chat-completions-route.test.ts`

9. Files: `apps/api/src/runtime/services.ts`, `apps/api/src/providers/registry.ts`, relevant provider tests
   Change: Extend startup readiness and provider status handling to include OpenRouter, OpenAI chat, Gemini, and Anthropic using safe metadata endpoints where available.
   Verify: `pnpm --filter @hive/api exec vitest run test/providers/provider-status.test.ts test/providers/provider-registry.test.ts`

10. Files: `README.md`, `CHANGELOG.md`, `docs/architecture/system-architecture.md`, `docs/plans/completed/2026-03-14-provider-agnostic-zero-cost-chat-design.md`
    Change: Document the provider-backed zero-cost architecture, the new provider env vars, the OpenAI-compatibility boundary, and the no-paid-fallback rule for zero-cost traffic.
    Verify: `rg -n "OPENROUTER_|GEMINI_|ANTHROPIC_|guest-free|zero-cost|OpenAI-compatible" README.md CHANGELOG.md docs/architecture/system-architecture.md docs/plans/completed/2026-03-14-provider-agnostic-zero-cost-chat-design.md`

11. Files: touched API files, touched docs, touched web files if any web assertions need adjustment
    Change: Run the required verification suite, fix regressions, and confirm the final implementation against the Docker-local workflow expectations.
    Verify: `pnpm --filter @hive/api test`
    Verify: `pnpm --filter @hive/api build`
    Verify: `pnpm --filter @hive/web test`
    Verify: `NEXT_PUBLIC_API_BASE_URL=http://127.0.0.1:8080 NEXT_PUBLIC_SUPABASE_URL=http://127.0.0.1:54321 NEXT_PUBLIC_SUPABASE_ANON_KEY=test-supabase-anon-key pnpm --filter @hive/web build`

## Risks & mitigations

- Risk: provider overlap creates ambiguous routing and hidden billing changes.
  Mitigation: keep public virtual-model pricing separate from internal provider offers and test zero-cost routing explicitly.
- Risk: zero-cost traffic accidentally falls through to paid providers during outages.
  Mitigation: add a hard zero-cost eligibility check in the registry and targeted tests that assert no paid fallback.
- Risk: Anthropic translation diverges from the OpenAI-style public contract.
  Mitigation: isolate Anthropic behind a native adapter with dedicated translation tests.
- Risk: env expansion makes local setup brittle.
  Mitigation: keep defaults explicit in `.env.example`, preserve current provider timeout/retry conventions, and document every new variable in the same change.

## Rollback plan

- Revert the provider-offer catalog and restored zero-cost routing changes together.
- Restore `guest-free` to the previous static model catalog only if the provider-backed rollout has to be abandoned temporarily.
- Revert added provider env parsing and adapter wiring in one pass so startup configuration returns to the previous stable shape.
- Re-run `pnpm --filter @hive/api test`, `pnpm --filter @hive/api build`, and `pnpm --filter @hive/web test` after rollback to confirm the prior behavior is restored.
