## Goal
Fix OpenRouter-related test failures by allowing createRuntimeServices to accept an optional environment object and providing a safe default in tests.

## Assumptions
- The test failures are due to createRuntimeServices calling getEnv() which throws when required OpenRouter env vars are missing.
- Modifying createRuntimeServices to accept an optional env is a safe and common pattern for testing.

## Plan
1.  **Update createRuntimeServices**
    -   **File:** apps/api/src/runtime/services.ts
    -   **Change:** Modify createRuntimeServices to accept an optional `env?: AppEnv` argument. If provided, use it; otherwise, call getEnv().
    -   **Verify:** pnpm --filter @hive/api build

2.  **Fix failing tests by providing mock env**
    -   **Files:** 
        - apps/api/test/domain/credits-ledger.test.ts
        - apps/api/test/domain/payment-service.test.ts
        - apps/api/test/routes/users-routes.test.ts
        - apps/api/test/routes/users-settings-routes.test.ts
    -   **Change:** Update createRuntimeServices() calls to include a mock environment object that satisfies the AppEnv type, especially the new providers.openrouter configuration.
    -   **Verify:** pnpm --filter @hive/api test

## Risks & mitigations
-   **Risk:** Missing other required env vars in mock objects.
    -   **Mitigation:** Use getEnv() as a base if possible, or ensure mock objects are comprehensive enough for the test case.
