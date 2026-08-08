import { test, expect } from "@playwright/test";

import { requireEnv } from "./support/require-env";

const CONTROL_PLANE_URL =
  process.env.CONTROL_PLANE_URL ?? "http://localhost:8081";

test("crafting a body with another tenant_id is denied and audited CRITICAL", async ({
  playwright,
}) => {
  const tenantBID = requireEnv("E2E_TENANT_B_ID");
  const dbURL = requireEnv("HIVE_TEST_DB_URL");
  // See 03-tenant-isolation.spec.ts for why this is a bearer token rather
  // than an Open WebUI storage-state file.
  const userAToken = requireEnv("E2E_USER_A_JWT");

  const api = await playwright.request.newContext({
    extraHTTPHeaders: { Authorization: `Bearer ${userAToken}` },
  });

  // Anchor the audit-log scan to the request boundary. The old
  // `now() - interval '2 minutes'` form could be satisfied by an unrelated
  // CROSS_TENANT_ATTEMPT row from a concurrent job on the shared project.
  const startedAt = new Date();
  try {
    // User A is NOT a member of tenant B.
    const resp = await api.post(`${CONTROL_PLANE_URL}/v1/tenants/switch`, {
      data: { tenant_id: tenantBID },
    });
    expect(resp.status()).toBe(403);
    const body = await resp.json();
    expect(body.error?.code).toBe("CROSS_TENANT");
  } finally {
    await api.dispose();
  }

  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  const pgMod: any = await import("pg").catch(() => null);
  // Not a skip: see 04-tenant-switch.spec.ts.
  expect(pgMod, "pg module not installed").not.toBeNull();

  const db = new pgMod.Client({ connectionString: dbURL });
  await db.connect();
  try {
    const audits = await db.query(
      `SELECT action, severity FROM public.audit_log
        WHERE ts >= $1 AND action = 'CROSS_TENANT_ATTEMPT'`,
      [startedAt],
    );
    expect(audits.rowCount).toBeGreaterThan(0);
    expect(audits.rows[0].severity).toBe("CRITICAL");
  } finally {
    await db.end();
  }
});
