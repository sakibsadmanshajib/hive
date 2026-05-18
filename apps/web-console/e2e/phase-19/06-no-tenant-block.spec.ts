import { test, expect } from "@playwright/test";

const EDGE_API_URL = process.env.EDGE_API_URL ?? "http://localhost:8080";

test("user with no tenant gets NO_TENANT", async ({ request }) => {
  // The orphan test account has no tenant_users row by design.
  const tok = process.env.E2E_ORPHAN_JWT;
  if (!tok) test.skip(true, "E2E_ORPHAN_JWT not set");

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
