## Goal
Remove all traces of OpenRouter and CostCalculator implementation that were inadvertently added/restored.

## Plan
1.  **Remove Files**
    - `apps/api/src/providers/openrouter-client.ts`
    - `apps/api/src/providers/cost-calculator.ts`
    - `apps/api/test/providers/openrouter-client.test.ts`
    - `apps/api/test/providers/cost-calculator.test.ts`

2.  **Revert Changes to core files**
    - `apps/api/src/providers/types.ts`: Remove "openrouter" from ProviderName.
    - `apps/api/src/config/env.ts`: Remove openrouter config.
    - `apps/api/src/runtime/services.ts`: Remove openrouter imports and registration.

3.  **Clean up tests**
    - Remove openrouter mock config from affected tests.

4.  **Update .gitignore**
    - Add `artifacts/` to `.gitignore`.

5.  **Update AGENTS.md**
    - Clarify artifacts are local-only.
