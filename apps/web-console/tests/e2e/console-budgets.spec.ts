import { expect, test, type Page } from "@playwright/test";
import { reseedFixtures } from "./support/fixture-reset";
import {
  E2E_VERIFIED_EMAIL as VERIFIED_EMAIL,
  E2E_VERIFIED_PASSWORD as VERIFIED_PASSWORD,
} from "./support/e2e-auth-creds";
import { switchToWorkspace } from "./support/workspace-switch";

// Seeded workspace display names (tests/e2e/support/e2e-fixture-seed.mjs): the
// verified user owns the first and is a plain member of the second.
const OWNED_WORKSPACE = "E2E Verified Workspace";
const MEMBER_WORKSPACE = "E2E Shared Workspace";
const READ_ONLY_NOTICE = "Only the workspace owner can edit budget caps.";

// Phase 14 FIX-14-28 — /console/billing/budget E2E.
//
// Asserts the owner-gated workspace budget surface:
//   - heading + form render
//   - soft + hard cap inputs visible
//
// Save round-trip + non-owner read-only assertion are deferred to a CI run
// against a workspace where the verified tester is an owner; the smoke
// surface here is order-stable across env states.

const HAS_CREDS = Boolean(VERIFIED_EMAIL && VERIFIED_PASSWORD);

async function signIn(page: Page, email: string, password: string) {
  await page.goto("/auth/sign-in");
  await page.locator("#email").fill(email);
  await page.locator("#password").fill(password);
  await page.click('button[type="submit"]');
  await page.waitForURL((url) => url.pathname.startsWith("/console"), {
    timeout: 25_000,
  });
}

// Every test here drives several full server-rendered navigations (sign-in,
// a workspace switch, the budget page, a reload), each one a control-plane
// round trip. The 30s default is a budget for a single page, so on a slower
// runner the clock runs out before the assertions are reached and the result
// is decided by machine speed rather than by the product. A larger budget
// cannot turn a failing assertion green; it only stops a timeout from
// standing in for one.
test.describe.configure({ mode: "serial", timeout: 120_000 });

test.beforeEach(async ({}, testInfo) => {
  if (!HAS_CREDS) return;
  await reseedFixtures(testInfo);
});

test.describe("/console/billing/budget — workspace budget caps (Phase 14)", () => {
  test.skip(!HAS_CREDS, "E2E_VERIFIED_EMAIL/PASSWORD not set");

  test("budget page renders", async ({ page }) => {
    await signIn(page, VERIFIED_EMAIL, VERIFIED_PASSWORD);
    await page.goto("/console/billing/budget");

    await expect(
      page.getByRole("heading", { name: /budget/i }).first(),
    ).toBeVisible({ timeout: 15_000 });

    // Form primitives — soft + hard cap inputs present regardless of role.
    await expect(page.locator("#budget-soft-cap")).toBeVisible();
    await expect(page.locator("#budget-hard-cap")).toBeVisible();
  });

  // Issue #796. This used to assert toBeAttached() on the two cap inputs and
  // call it read-only enforcement. toBeAttached() is equally true of an
  // enabled field, so the assertion held whether or not a member could edit
  // the workspace budget, and it never touched the state that decides.
  //
  // The seeded verified user is OWNER of "E2E Verified Workspace" and MEMBER of
  // "E2E Shared Workspace" (tests/e2e/support/e2e-fixture-seed.mjs), and
  // app/console/billing/budget/page.tsx passes readOnly={!isOwner}. So both
  // sides of the gate are reachable by one signed-in user, and neither is
  // asserted by branching on whatever role the run happens to produce.
  test("budget caps are editable for the owner and disabled for a member", async ({
    page,
  }) => {
    await signIn(page, VERIFIED_EMAIL, VERIFIED_PASSWORD);

    await switchToWorkspace(page, OWNED_WORKSPACE);
    await page.goto("/console/billing/budget");
    await expect(
      page.getByRole("heading", { name: /budget/i }).first(),
    ).toBeVisible({ timeout: 15_000 });

    // Positive control: without it, a page that disabled the form for everyone
    // would satisfy the member assertions below.
    await expect(page.locator("#budget-soft-cap")).toBeEnabled();
    await expect(page.locator("#budget-hard-cap")).toBeEnabled();
    await expect(
      page.getByRole("button", { name: /save budget/i }),
    ).toBeEnabled();
    await expect(page.getByText(READ_ONLY_NOTICE)).toHaveCount(0);

    await switchToWorkspace(page, MEMBER_WORKSPACE);
    await page.goto("/console/billing/budget");
    await expect(
      page.getByRole("heading", { name: /budget/i }).first(),
    ).toBeVisible({ timeout: 15_000 });

    await expect(page.locator("#budget-soft-cap")).toBeDisabled();
    await expect(page.locator("#budget-hard-cap")).toBeDisabled();
    await expect(
      page.getByRole("button", { name: /save budget/i }),
    ).toBeDisabled();
    await expect(page.getByText(READ_ONLY_NOTICE)).toBeVisible();

    // The role that produced the read-only form came from the server, so it
    // has to survive a reload. A client-only guard would not.
    await page.reload();
    await expect(page.locator("#budget-soft-cap")).toBeDisabled();

    // Leave the session on the owned workspace for whatever runs next.
    await switchToWorkspace(page, OWNED_WORKSPACE);
  });
});
