import { test, expect } from "@playwright/test";

import { requireEnv } from "./support/require-env";

const EDGE_API_URL = process.env.EDGE_API_URL ?? "http://localhost:8080";

test("user with no tenant gets NO_TENANT", async ({ request }) => {
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
  expect(resp.status()).toBe(403);
  const body = await resp.json();
  expect(body.error?.code).toBe("NO_TENANT");
});
