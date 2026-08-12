import { test, expect } from "@playwright/test";

import { requireEnv } from "./support/require-env";

const CONTROL_PLANE_URL =
  process.env.CONTROL_PLANE_URL ?? "http://localhost:8081";

test("cross-tenant read returns 403 CROSS_TENANT", async ({ playwright }) => {
  // The endpoint this asserts does not exist. Its first execution (issue #659)
  // answered 404, and control-plane registers no handler under
  // /v1/tenants/{id}/settings: grep the router in
  // apps/control-plane/cmd/server/main.go and the only tenant-scoped route
  // there is /v1/tenants/switch. So this spec was written against a planned
  // surface that was never built, and passing it would need that surface, not
  // a change here.
  //
  // Marked rather than deleted, and rather than quietly repointed at the
  // switch route (07 already covers that, and a second copy of an assertion is
  // not the same as coverage of this one). The cross-tenant denial that IS
  // implemented is covered by 07 here and by TestSwitch_NonMember_403CrossTenant
  // in apps/control-plane/internal/tenants, which runs in a required check.
  // Building the settings surface, or dropping this spec, is a product call
  // tracked on #659.
  test.fixme(true, "control-plane has no /v1/tenants/{id}/settings route; see the comment above");
  const tenantBID = requireEnv("E2E_TENANT_B_ID");
  // User A's own Supabase access token, minted by scripts/seed-phase19-e2e.py.
  // This used to load a storage-state file written by auth.setup.ts, which
  // signs in through Open WebUI. That made a plain control-plane HTTP
  // assertion depend on a browser journey against a service this job does
  // not boot, and it is a large part of why the spec never ran. The bearer
  // token is also the credential the production caller actually presents.
  const userAToken = requireEnv("E2E_USER_A_JWT");

  const a = await playwright.request.newContext({
    extraHTTPHeaders: { Authorization: `Bearer ${userAToken}` },
  });
  try {
    const resp = await a.get(
      `${CONTROL_PLANE_URL}/v1/tenants/${tenantBID}/settings`,
    );
    expect(resp.status()).toBe(403);
    const body = await resp.json();
    expect(body.error?.code).toBe("CROSS_TENANT");
  } finally {
    await a.dispose();
  }
});
