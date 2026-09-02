import { expect, test, type Page } from "@playwright/test";
import { reseedFixtures } from "./support/fixture-reset";

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

function escapeRegExp(value: string): string {
  return value.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
}

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

  // Seed before signing in, rather than relying on another spec having done it.
  //
  // This account used to be a long-lived one on the shared hosted project, so
  // the spec could sign in without seeding anything and did. Now that CI runs
  // against a throwaway Supabase, the account exists only because some spec
  // seeded it earlier in the same process, which today means auth-shell.spec.ts
  // happening to sort before this file under `workers: 1`. That is a real
  // dependency on filename order, and the failure it produces if the order ever
  // changes (rename, skip, or running this file on its own) is a 25 second
  // waitForURL timeout inside signIn() that names nothing, which is the exact
  // shape this branch removed elsewhere.
  //
  // reseedFixtures is idempotent and is what the other console specs already
  // call, so this is the established pattern rather than a new mechanism. It
  // charges its own wall time to the hook instead of to the test's budget.
  test.beforeEach(async ({ page }, testInfo) => {
    await reseedFixtures(testInfo);
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
    // The read-only row label. It used to read "Managed by your administrator",
    // which was also the title of the 403 EmptyState, so the bare string passed
    // on a fully denied page too; issue #1660 renamed the row label, and this
    // stays scoped to a list item anyway since the wall renders none.
    await expect(
      page.locator("li", { hasText: "Managed by the platform" }).first()
    ).toBeVisible();

    const toggles = page.getByRole("switch");
    await expect(toggles).not.toHaveCount(0);
  });

  // Issue #796. This used to be the tail of the test above: assert the first
  // toggle carries aria-checked matching /true|false/. That regex is unanchored
  // and matches the only two values the attribute can hold, so it passed on a
  // toggle nobody had ever clicked, and the spec never clicked one. A control
  // that renders and does nothing was indistinguishable from a working one.
  //
  // The reload is the part that matters. Every toggle in this app flips
  // optimistically in local state before the PUT resolves, so an assertion
  // taken straight after the click passes even when the write never lands. The
  // page has to be reloaded and the value re-read from the server for the flip
  // to mean anything. No spec in this repository reloaded a page before this
  // one, which is why no setting anywhere had a persistence test.
  test("a feature gate toggle flips, persists across a reload, and flips back", async ({
    page,
  }) => {
    await page.goto("/console/feature-gates");
    await expect(
      page.getByRole("heading", { name: "Feature gates" })
    ).toBeVisible();

    // Only manageable gates render a switch; unmanageable ones render the
    // "Managed by the platform" label instead, so any switch on the page is
    // one this viewer is allowed to write.
    const toggle = page.getByRole("switch").first();
    await expect(toggle).toBeVisible();

    // aria-label is `${label}: enabled|disabled`, unique per row, and survives
    // the reload that invalidates the locator's element handle.
    const gateLabel = ((await toggle.getAttribute("aria-label")) ?? "").replace(
      /: (enabled|disabled)$/,
      ""
    );
    expect(gateLabel).not.toBe("");
    const byGate = () =>
      page.getByRole("switch", { name: new RegExp(`^${escapeRegExp(gateLabel)}: `) });

    const before = await toggle.getAttribute("aria-checked");
    expect(before === "true" || before === "false").toBe(true);
    const flipped = before === "true" ? "false" : "true";

    await toggle.click();
    // Optimistic flip lands immediately; this alone proves nothing persisted.
    await expect(byGate()).toHaveAttribute("aria-checked", flipped);
    // The row leaves "saving" (the switch is disabled while the PUT is in
    // flight), so waiting for it to be enabled again waits for the write.
    await expect(byGate()).toBeEnabled();

    await page.reload();
    await expect(
      page.getByRole("heading", { name: "Feature gates" })
    ).toBeVisible();
    await expect(byGate()).toHaveAttribute("aria-checked", flipped);

    // Restore the workspace to the state this spec found it in, and prove the
    // control moves in both directions rather than latching once.
    await byGate().click();
    await expect(byGate()).toBeEnabled();
    await page.reload();
    await expect(byGate()).toHaveAttribute("aria-checked", before!);
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
