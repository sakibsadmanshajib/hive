import { E2E_VERIFIED_EMAIL } from "./e2e-auth-creds";

// Spec-side twin of `resetProfileBetweenSpecs` in `e2e-auth-fixtures.mjs`.
// Lives in a .ts module because Playwright's transform compiles spec imports
// to CommonJS, and importing a .mjs from a spec makes Node evaluate that
// CJS output in ES module scope ("exports is not defined"). The .mjs copy
// remains for CLI (child process) callers only.
export async function resetProfileBetweenSpecs(testInfo?: {
  email?: string;
}): Promise<unknown | null> {
  const url = process.env.E2E_FIXTURE_URL;
  const secret = process.env.E2E_FIXTURE_SECRET;
  if (!url || !secret) {
    return null;
  }
  const targetEmail = testInfo?.email ?? E2E_VERIFIED_EMAIL;
  const response = await fetch(url, {
    method: "POST",
    headers: {
      "X-E2E-Secret": secret,
      "Content-Type": "application/json",
    },
    body: JSON.stringify({
      action: "reset-profile",
      email: targetEmail,
    }),
  });
  const text = await response.text();
  let data: unknown = null;
  try {
    data = text ? JSON.parse(text) : null;
  } catch {
    data = { raw: text };
  }
  if (!response.ok) {
    const message =
      (data as { error?: string } | null)?.error ??
      `${response.status} ${response.statusText}`;
    throw new Error(`resetProfileBetweenSpecs failed: ${message}`);
  }
  return data;
}
