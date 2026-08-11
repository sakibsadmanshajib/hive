import { test, expect } from "@playwright/test";

import { requireEnv } from "./support/require-env";

const EDGE_API_URL = process.env.EDGE_API_URL ?? "http://localhost:8080";

// What this asserts changed the first time it ran (issue #659). It was written
// for 403 NO_TENANT, the code chat dispatch returns for a caller whose context
// carries no tenant (apps/edge-api/internal/chat/dispatch.go, covered by
// TestDispatchNoTenantReturnsNoTenant). A bearer JWT never reaches that branch:
// apps/edge-api/internal/auth/middleware.go rejects a signature-valid token
// with no tenant claim at the door, as documented defence in depth, so the
// answer is 401 UNAUTHENTICATED. Both deny, and the guarantee this spec exists
// for (an account with no tenant cannot reach the model) holds either way. It
// asserts the denial the implementation actually gives rather than the one the
// phase-19 plan predicted, because a test that asserts a contract nothing
// implements is not coverage.
test("user with no tenant is denied at the edge", async ({ request }) => {
  // The orphan test account has no tenant_users row by design.
  // scripts/seed-phase19-e2e.py creates it, strips any membership a trigger
  // or an earlier run left behind, and refuses to continue unless the
  // readback finds none, so this token really does carry no tenant.
  const tok = requireEnv("E2E_ORPHAN_JWT");

  const resp = await request.post(
    `${EDGE_API_URL}/v1/chat/completions`,
    {
      headers: { Authorization: `Bearer ${tok}` },
      data: {
        model: "gpt-4o-mini",
        messages: [{ role: "user", content: "hi" }],
      },
    },
  );
  expect(resp.status()).toBe(401);
  const body = await resp.json();
  expect(body.error?.code).toBe("UNAUTHENTICATED");
});
