import { test as setup } from "@playwright/test";

import { signInWithHive } from "./sign-in-with-hive";

const OWUI_URL = process.env.OWUI_URL ?? "http://localhost:3003";
const STATE_A = "e2e/phase-19/.auth/user-a.json";
const STATE_B = "e2e/phase-19/.auth/user-b.json";

setup("authenticate user A (tenant T1)", async ({ page }) => {
  // Run 28681926134: consent can load first, then its client-side session
  // check bounces to sign-in -- give the retry-heavy journey real time to
  // survive that bounce instead of the 30s test default.
  setup.setTimeout(180_000);

  const email = process.env.E2E_USER_A_EMAIL;
  const password = process.env.E2E_USER_A_PASSWORD;
  if (!email || !password) {
    setup.skip(true, "E2E_USER_A_* env not set");
    return;
  }
  await page.goto(`${OWUI_URL}/`);
  await signInWithHive(page, email, password, new URL(OWUI_URL).origin);
  await page.context().storageState({ path: STATE_A });
});

setup("authenticate user B (tenant T2)", async ({ page }) => {
  setup.setTimeout(180_000);

  const email = process.env.E2E_USER_B_EMAIL;
  const password = process.env.E2E_USER_B_PASSWORD;
  if (!email || !password) {
    setup.skip(true, "E2E_USER_B_* env not set");
    return;
  }
  await page.goto(`${OWUI_URL}/`);
  await signInWithHive(page, email, password, new URL(OWUI_URL).origin);
  await page.context().storageState({ path: STATE_B });
});
