import { test, expect } from "@playwright/test";

import { requireEnv } from "./support/require-env";

const EDGE_API_URL = process.env.EDGE_API_URL ?? "http://localhost:8080";

// The one value in this directory that CI genuinely cannot produce, spelled
// out so the failure reads as a known constraint rather than a forgotten
// secret. Enabling this spec needs a decision, not a workflow line.
const CANNOT_MINT =
  "CI cannot mint this. edge-api validates bearer tokens against the Supabase " +
  "project's JWKS (apps/edge-api/internal/auth/jwt_supabase.go) and refuses a " +
  "non-https JWKS URL outright (apps/edge-api/cmd/server/main.go, " +
  "loadJWTAuthEnv), so a token this job signs itself is rejected as an invalid " +
  "signature and never reaches the expiry branch. Only Supabase holds the key " +
  "that could sign one, and the project's access tokens outlive the job, so " +
  "waiting one out is not an option either. Until that is resolved this spec " +
  "has no coverage, and that fact belongs in a failing run rather than in a " +
  "green one (issue #659).";

test("expired JWT returns 401 JWT_EXPIRED", async ({ request }) => {
  const expired = requireEnv("E2E_EXPIRED_JWT", CANNOT_MINT);

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
