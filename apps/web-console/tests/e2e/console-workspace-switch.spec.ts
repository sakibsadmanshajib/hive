import { expect, test, type Page } from "@playwright/test";
import { reseedFixtures } from "./support/fixture-reset";

import {
  E2E_VERIFIED_EMAIL as VERIFIED_EMAIL,
  E2E_VERIFIED_PASSWORD as VERIFIED_PASSWORD,
} from "./support/e2e-auth-creds";
import {
  accountIdFor,
  switchToWorkspace,
  workspaceSelect,
} from "./support/workspace-switch";

// Issue #794. PR #786 fixed a sidebar workspace control that rendered but did
// nothing. The fix was one onChange handler, and deleting it again left all 423
// unit tests green, so the fix shipped with no guard at any level.
//
// The unit test (tests/unit/workspace-switcher.test.tsx) proves the change
// event submits the chosen account. This proves the half a unit test cannot:
// that the switch is still in effect after the page is reloaded, because
// /console/account-switch wrote it to the hive_account_id cookie and the server
// re-rendered from that, rather than the value living in client state until the
// next navigation throws it away.
//
// Before this spec, no test in this repository called page.reload(), so no
// setting in the product had a persistence test at all.

const HAS_CREDS = Boolean(VERIFIED_EMAIL && VERIFIED_PASSWORD);

// Seeded memberships (tests/e2e/support/e2e-fixture-seed.mjs): the verified
// user belongs to both, which is what makes the switcher render as a control
// rather than a static label.
const OWNED_WORKSPACE = "E2E Verified Workspace";
const MEMBER_WORKSPACE = "E2E Shared Workspace";

async function signIn(page: Page): Promise<void> {
  await page.goto("/auth/sign-in");
  await page.locator("#email").fill(VERIFIED_EMAIL);
  await page.locator("#password").fill(VERIFIED_PASSWORD);
  await page.click('button[type="submit"]');
  await page.waitForURL((url) => url.pathname.startsWith("/console"), {
    timeout: 25_000,
  });
}

// Every test here drives several full server-rendered navigations (sign-in,
// the console, a switch, a reload), each one a control-plane round trip. The
// 30s default is a budget for a single page, so on a slower runner the clock
// runs out before the assertions are reached and the result is decided by
// machine speed rather than by the product. A larger budget cannot turn a
// failing assertion green; it only stops a timeout from standing in for one.
test.describe.configure({ mode: "serial", timeout: 120_000 });

test.beforeEach(async ({}, testInfo) => {
  if (!HAS_CREDS) return;
  await reseedFixtures(testInfo);
});

test.describe("sidebar workspace switcher (issue #794)", () => {
  test.skip(!HAS_CREDS, "E2E_VERIFIED_EMAIL/PASSWORD not set");

  test("switching workspace takes effect and survives a reload", async ({
    page,
  }) => {
    await signIn(page);
    await page.goto("/console");

    // Start from a known workspace so the target below is genuinely a change.
    await switchToWorkspace(page, OWNED_WORKSPACE);
    const ownedId = await accountIdFor(page, OWNED_WORKSPACE);
    const memberId = await accountIdFor(page, MEMBER_WORKSPACE);
    expect(memberId).not.toBe(ownedId);
    await expect(workspaceSelect(page)).toHaveValue(ownedId);

    await switchToWorkspace(page, MEMBER_WORKSPACE);

    // This value is server-rendered from the cookie the switch handler set:
    // the select the test wrote into was destroyed by the form's navigation.
    await page.reload();
    await expect(workspaceSelect(page)).toHaveValue(memberId);
    await expect(
      page.getByRole("option", { name: `${MEMBER_WORKSPACE} (current)` })
    ).toHaveCount(1);

    // Both directions, and back to where the run started.
    await switchToWorkspace(page, OWNED_WORKSPACE);
    await page.reload();
    await expect(workspaceSelect(page)).toHaveValue(ownedId);
  });
});
