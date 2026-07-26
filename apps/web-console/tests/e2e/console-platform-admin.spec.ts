import { expect, test, type Page } from "@playwright/test";

// Guards the platform-admin panels against a silently wrong control-plane.
//
// The console reads its permission set from the control-plane's
// /api/v1/viewer. When CONTROL_PLANE_BASE_URL names a host that is reachable
// but running an older build, that response comes back without
// platform.admin, and Feature gates plus Marketplace degrade to the
// "Admin access required" empty state for an account that genuinely is a
// platform admin. Nothing errors and every healthcheck stays green, so only
// an assertion on the rendered panel catches it.
//
// Deliberately asserts the symptom rather than the configured hostname: any
// future cause of a permission-starved viewer fails this spec too.
//
// Credentials come from the environment with no committed fallback, since the
// platform-admin fixture is provisioned by scripts/seed-demo-owner.py, which
// rotates its password on every run. Point PLAYWRIGHT_BASE_URL at the
// deployment under test.
const EMAIL = process.env.E2E_PLATFORM_ADMIN_EMAIL ?? "";
const PASSWORD = process.env.E2E_PLATFORM_ADMIN_PASSWORD ?? "";
const HAS_CREDS = Boolean(EMAIL && PASSWORD);

async function signIn(page: Page): Promise<void> {
  await page.goto("/auth/sign-in");
  await page.locator("#email").fill(EMAIL);
  await page.locator("#password").fill(PASSWORD);
  await page.click('button[type="submit"]');
  await page.waitForURL((url) => url.pathname.startsWith("/console"), {
    timeout: 25_000,
  });
}

test.describe("platform-admin panels", () => {
  test.skip(
    !HAS_CREDS,
    "set E2E_PLATFORM_ADMIN_EMAIL and E2E_PLATFORM_ADMIN_PASSWORD to run"
  );

  test.beforeEach(async ({ page }) => {
    await signIn(page);
  });

  test("feature gates renders toggles, not the admin-required state", async ({
    page,
  }) => {
    await page.goto("/console/feature-gates");
    await expect(
      page.getByRole("heading", { name: "Feature gates" })
    ).toBeVisible();

    await expect(page.getByText("Admin access required")).toHaveCount(0);

    const toggles = page.getByRole("switch");
    expect(await toggles.count()).toBeGreaterThan(0);
    await expect(toggles.first()).toHaveAttribute("aria-checked", /true|false/);
  });

  test("marketplace renders the curate form", async ({ page }) => {
    await page.goto("/console/marketplace");
    await expect(page.getByText("Admin access required")).toHaveCount(0);
    await expect(
      page.getByRole("button", { name: "Curate entry" })
    ).toBeVisible();
  });
});
