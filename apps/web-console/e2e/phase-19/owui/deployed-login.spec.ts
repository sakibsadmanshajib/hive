import { test, expect } from "@playwright/test";

// Deployed-origin OIDC sign-in smoke. owui.setup.ts already covers this
// journey, but it also installs the hive_jwt_forward Function and writes
// storageState, and both of those mutate whatever they point at. Neither is
// acceptable against a live deployment, so this spec logs in and asserts,
// nothing else.
//
// It exists because the consent page is served from Supabase's site_url,
// which is a different origin from the chat surface (chat-hive.scubed.co ->
// console-hive.scubed.co -> back). The nightly exercises that hop with the
// chat surface on localhost; this one exercises it with both sides deployed,
// which is the only configuration a real user ever sees.
const OWUI_URL = process.env.OWUI_URL ?? "";

// ponytail: string comparison, not a DNS resolve. Covers every loopback form
// a config here could plausibly use; a hostname that resolves to loopback
// only through DNS would slip past, and that would be a deliberately strange
// way to point this at CI.
function isLoopback(hostname: string): boolean {
  const host = hostname.replace(/^\[|\]$/g, "").toLowerCase();
  return (
    host === "localhost" ||
    host.endsWith(".localhost") ||
    host === "::1" ||
    host === "0.0.0.0" ||
    host.startsWith("127.")
  );
}

test("OIDC sign-in completes against the deployed origin", async ({ page }) => {
  if (!OWUI_URL || isLoopback(new URL(OWUI_URL || "http://localhost").hostname)) {
    test.skip(true, "OWUI_URL is not a deployed origin");
    return;
  }

  const email = process.env.OWUI_E2E_EMAIL;
  const password = process.env.OWUI_E2E_PASSWORD;
  if (!email || !password) {
    test.skip(true, "OWUI_E2E_EMAIL/OWUI_E2E_PASSWORD not set");
    return;
  }

  const owuiOrigin = new URL(OWUI_URL).origin;
  await page.goto("/auth");

  const hiveButton = page.getByRole("button", { name: /continue with hive/i });
  await expect(hiveButton).toBeVisible({ timeout: 30_000 });
  // The login page animates continuously, so Playwright's click-stability
  // check never settles and force-click still requires the element in the
  // viewport. dispatchEvent fires the handler regardless of geometry.
  await hiveButton.dispatchEvent("click");

  const emailBox = page.getByRole("textbox", { name: /email/i });
  const passwordBox = page.getByRole("textbox", { name: /password/i });
  const approveButton = page.getByRole("button", { name: /approve/i });
  /*
   * Either role, because the control is an <a>.
   *
   * Open WebUI renders both New Chat controls (the collapsed rail's icon and
   * the expanded sidebar's row) as anchors with an aria-label, so their
   * implicit role is `link` and a `button` query can never match. This
   * readiness signal has therefore been failing since the control changed
   * shape, taking every OWUI end-to-end run with it: the sign-in it guards
   * actually succeeds, and the run fails 60 seconds later looking for an
   * element that cannot exist. e2e/chat-coverage/surfaces.ts already matches
   * both roles for exactly this reason; these two call sites were missed.
   */
  const newChatButton = page
    .getByRole("button", { name: /new chat/i })
    .or(page.getByRole("link", { name: /new chat/i }))
    .first();

  // Consent renders first and only bounces to sign-in once its client-side
  // session check returns empty, so a URL check can observe the transient
  // consent pathname and wrongly conclude we are past login. Wait on the DOM.
  await expect(emailBox.or(approveButton)).toBeVisible({ timeout: 45_000 });

  // Fill and submit are fused into one retry unit, ported from owui.setup.ts:
  // React hydration can remount the controlled inputs after a fill has
  // already verified as stuck, wiping them, so a submit fired after that
  // remount hits an empty form (run 28680373668). Every attempt must re-fill
  // first. Kept for the deployed path too: the documented trigger was a dev
  // build, but a production build is not proof the remount cannot happen, and
  // a tunnelled cross-origin hop has more timing variance, not less.
  for (let i = 0; i < 6; i++) {
    if (!(await emailBox.isVisible().catch(() => false))) break;
    await emailBox.fill(email, { timeout: 5_000 });
    await passwordBox.fill(password, { timeout: 5_000 });
    if (
      (await emailBox.inputValue()) !== email ||
      (await passwordBox.inputValue()) !== password
    ) {
      continue;
    }
    try {
      await page
        .getByRole("button", { name: /continue/i })
        .click({ timeout: 5_000 });
    } catch {
      // button may already be gone if a prior click's navigation landed late
    }
    // #554: scrub the password field the instant it has done its job.
    // Playwright's error-context ARIA snapshot is captured at the moment
    // the assertion below times out, and that snapshot serializes live
    // input values -- if this submit didn't navigate away, the password
    // would otherwise still be sitting in the DOM for the one check that
    // can fail. No-op if the click already navigated past this form.
    await passwordBox.fill("").catch(() => {});
    try {
      await expect(approveButton.or(newChatButton)).toBeVisible({
        timeout: 8_000,
      });
      break;
    } catch {
      // retry
    }
  }

  // Supabase auto-approves a client the user has already granted, so consent
  // shows at most once per user and client and must stay optional (run
  // 28682845959). Same late-navigation guard as above around the click.
  for (let i = 0; i < 5; i++) {
    if (new URL(page.url()).origin === owuiOrigin) break;
    if (!(await approveButton.isVisible().catch(() => false))) break;
    try {
      await approveButton.click({ timeout: 5_000 });
    } catch {
      // button may already be gone if a prior click's navigation landed late
    }
    try {
      await page.waitForURL((u) => u.origin === owuiOrigin, { timeout: 8_000 });
      break;
    } catch {
      // retry
    }
  }

  await page.waitForURL((u) => u.origin === owuiOrigin, { timeout: 45_000 });
  await expect(newChatButton).toBeVisible({ timeout: 60_000 });
});
