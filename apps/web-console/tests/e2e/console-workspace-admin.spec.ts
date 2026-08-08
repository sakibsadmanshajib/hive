import { expect, test, type Page } from "@playwright/test";

// Guards the workspace-scoped admin panels against a silently wrong gate.
//
// Two failure modes have hit this pair of pages. First, the console reads its
// permission set and its gate lists from the control-plane, and when
// CONTROL_PLANE_BASE_URL named a host that was reachable but running an older
// build, both panels degraded to an access wall for an account that genuinely
// had the authority. Second, issue #758: the panels were gated on
// platform-admin, so the OWNER of a workspace could not manage the feature
// gates or the connector catalogue of that same workspace. Nothing errors in
// either case and every healthcheck stays green, so only an assertion on the
// rendered panel catches it.
//
// Deliberately asserts the symptom rather than the configured hostname: any
// future cause of an under-permissioned viewer fails this spec too.
//
// Credentials come from the environment with no committed fallback. Point
// PLAYWRIGHT_BASE_URL at the deployment under test and supply an account that
// is OWNER of its workspace (scripts/seed-demo-owner.py provisions exactly
// that, and deliberately does not grant platform-admin).
const EMAIL = process.env.E2E_WORKSPACE_OWNER_EMAIL ?? process.env.E2E_PLATFORM_ADMIN_EMAIL ?? "";
const PASSWORD = process.env.E2E_WORKSPACE_OWNER_PASSWORD ?? process.env.E2E_PLATFORM_ADMIN_PASSWORD ?? "";
const HAS_CREDS = Boolean(EMAIL && PASSWORD);

async function signIn(page: Page): Promise<void> {
  await page.goto("/auth/sign-in");
  await page.locator("#email").fill(EMAIL);
  await page.locator("#password").fill(PASSWORD);
  await page.click("button[type=\"submit\"]");
  await page.waitForURL((url) => url.pathname.startsWith("/console"), {
    timeout: 25_000,
  });
}

test.describe("workspace admin panels", () => {
  test.skip(
    !HAS_CREDS,
    "set E2E_WORKSPACE_OWNER_EMAIL and E2E_WORKSPACE_OWNER_PASSWORD to run"
  );

  test.beforeEach(async ({ page }) => {
    await signIn(page);
  });

  test("feature gates renders toggles, not an access wall", async ({
    page,
  }) => {
    await page.goto("/console/feature-gates");
    await expect(
      page.getByRole("heading", { name: "Feature gates" })
    ).toBeVisible();

    await expect(page.getByText("Admin access required")).toHaveCount(0);
    // "Managed by your administrator" is the title of the 403 EmptyState as
    // well as the read-only row label, so the bare string passes on a fully
    // denied page too. Scope it to a gate row: the wall renders no list item.
    await expect(
      page.locator("li", { hasText: "Managed by your administrator" }).first()
    ).toBeVisible();

    const toggles = page.getByRole("switch");
    await expect(toggles).not.toHaveCount(0);
    await expect(toggles.first()).toHaveAttribute("aria-checked", /true|false/);
  });

  test("marketplace renders the catalog, not an access wall", async ({
    page,
  }) => {
    await page.goto("/console/marketplace");
    await expect(
      page.getByRole("heading", { name: "MCP and skills marketplace" })
    ).toBeVisible();
    await expect(page.getByText("Admin access required")).toHaveCount(0);
    // The heading renders on the 403 wall too. This line is the one thing only
    // the wall produces, so its absence is what separates the two states.
    await expect(
      page.getByText(
        "Ask your workspace owner or administrator if you need a connector enabled."
      )
    ).toHaveCount(0);
    // A non-403 load failure (app/console/marketplace/page.tsx loadFailed
    // branch) renders this same heading with no error and no 403 copy either,
    // so the assertions above alone pass on a broken marketplace too. Rule out
    // that branch by name, then require the one thing only a successful load
    // produces: an OWNER has no curation rights, so a populated catalog shows
    // a switch per entry and an empty one shows its own placeholder line
    // (see marketplace-manager.tsx); either is proof the fetch actually
    // succeeded, neither renders on the loadFailed or notPermitted EmptyState.
    await expect(
      page.getByText("Could not load the marketplace catalog")
    ).toHaveCount(0);
    const emptyCatalogNotice = page.getByText(
      "No connectors have been published for this workspace yet."
    );
    const catalogEntrySwitch = page.getByRole("switch").first();
    await expect(emptyCatalogNotice.or(catalogEntrySwitch)).toBeVisible();
  });
});
