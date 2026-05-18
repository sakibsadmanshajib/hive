import { test, expect } from "@playwright/test";

const CONTROL_PLANE_URL =
  process.env.CONTROL_PLANE_URL ?? "http://localhost:8081";

test("cross-tenant read returns 403 CROSS_TENANT", async ({ playwright }) => {
  const tenantBID = process.env.E2E_TENANT_B_ID;
  if (!tenantBID) test.skip(true, "E2E_TENANT_B_ID not set");

  const a = await playwright.request.newContext({
    storageState: "e2e/phase-19/.auth/user-a.json",
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
