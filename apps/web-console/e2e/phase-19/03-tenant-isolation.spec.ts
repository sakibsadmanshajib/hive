import { test, expect } from "@playwright/test";

import { requireEnv } from "./support/require-env";

const CONTROL_PLANE_URL =
  process.env.CONTROL_PLANE_URL ?? "http://localhost:8081";

test("cross-tenant read returns 403 CROSS_TENANT", async ({ playwright }) => {
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
