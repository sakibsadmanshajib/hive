import { test, expect } from "@playwright/test";

test.use({ storageState: "e2e/phase-19/.auth/user-a.json" });

const EDGE_API_URL = process.env.EDGE_API_URL ?? "http://localhost:8080";

test("expired JWT returns 401 JWT_EXPIRED", async ({ request }) => {
  const expired = process.env.E2E_EXPIRED_JWT;
  if (!expired) test.skip(true, "E2E_EXPIRED_JWT not set");

  const resp = await request.post(
    `${EDGE_API_URL}/v1/chat/completions`,
    {
      headers: { Authorization: `Bearer ${expired}` },
      data: {
        model: "gpt-4o-mini",
        messages: [{ role: "user", content: "hi" }],
      },
    },
  );
  expect(resp.status()).toBe(401);
  const body = await resp.json();
  expect(body.error?.code).toBe("JWT_EXPIRED");
});
