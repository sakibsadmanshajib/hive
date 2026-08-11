import { test, expect } from "@playwright/test";

const EDGE_API_URL = process.env.EDGE_API_URL ?? "http://localhost:8080";

// The one value in this directory that CI genuinely cannot produce, spelled
// out so the skip reads as a known constraint rather than a forgotten secret.
// Enabling this spec needs a decision, not a workflow line.
//
// Deliberately a skip with a stated reason rather than a throw. A throw here
// would put a permanently red step next to four specs that can genuinely go
// red, and a check that is always red is a check everybody learns to ignore,
// which costs more coverage than it buys. The reason travels with the skip in
// the run output and in the HTML report, so it is visible rather than silent,
// and issue #659 tracks closing it for real.
const CANNOT_MINT =
  "E2E_EXPIRED_JWT is not set and CI cannot mint it. edge-api validates bearer " +
  "tokens against the Supabase project's JWKS (apps/edge-api/internal/auth/" +
  "jwt_supabase.go) and refuses a non-https JWKS URL outright (apps/edge-api/" +
  "cmd/server/main.go, loadJWTAuthEnv), so a token this job signs itself is " +
  "rejected as an invalid signature and never reaches the expiry branch. Only " +
  "Supabase holds the key that could sign one, and the project's access tokens " +
  "outlive the job, so waiting one out is not an option either. Set the " +
  "variable to run this against a token you already hold (issue #659).";

test("expired JWT returns 401 JWT_EXPIRED", async ({ request }) => {
  test.skip(!process.env.E2E_EXPIRED_JWT, CANNOT_MINT);
  const expired = process.env.E2E_EXPIRED_JWT ?? "";

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
