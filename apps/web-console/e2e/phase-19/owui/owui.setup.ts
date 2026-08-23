import { test as setup, expect, type Page } from "@playwright/test";
import { execFileSync } from "node:child_process";
import path from "node:path";

import { signInWithHive } from "../sign-in-with-hive";

const STATE = "e2e/phase-19/owui/.auth/owui-user.json";
const OWUI_URL = process.env.OWUI_URL ?? "http://localhost:3002";

// #269: hive_jwt_forward is an Open WebUI Function (Admin > Functions), not
// a file the container auto-loads; there is no mount or env var that
// installs it. It must be created via the Functions REST API by an admin
// session.
//
// Where that admin session comes from changed with issue #748. It used to be
// free: the fixture signs in with tenant role OWNER, and a tenant OWNER was
// mapped onto Open WebUI admin. That mapping was a cross-tenant grant on a
// shared instance and is gone, along with upstream's unconditional
// first-user-becomes-admin promotion.
//
// The replacement on a real deployment is the control plane's platform
// attribute, and it is deliberately NOT what this fixture uses: granting
// accounts.is_platform_admin to a CI fixture would hand it the platform-wide
// authority behind credit minting and provider base-URL rewrites, in whatever
// database the job points at, which is the hazard #747 is about. This job's
// chat container is created and destroyed inside the job, so the fixture is
// promoted in that container's own database instead, after its login and
// before the install, by scripts/owui-promote-instance-admin.py. That
// promotion carries no Hive authority and is undone by that account's next
// login, which is exactly the lifetime this needs.
//
// #556: the create/update/activate/verify sequence itself is no longer
// written here. It lives in scripts/install-owui-jwt-forward.py, which the
// demo-box deploy runs on every deploy, so this suite exercises the exact
// code a deployment depends on instead of a lookalike that could drift from
// it. This file still owns the part only a browser can do: producing a real
// Open WebUI admin session through the full OAuth journey.
const HIVE_JWT_FORWARD_INSTALLER = path.resolve(
  __dirname,
  "../../../../../scripts/install-owui-jwt-forward-in-container.sh",
);

// #748: the fixture's Open WebUI admin role, granted in this job's own
// container and nowhere else. Same wrapper shape as the installer above, so
// both reach the container the way the deploy does.
const HIVE_INSTANCE_ADMIN_PROMOTER = path.resolve(
  __dirname,
  "../../../../../scripts/owui-promote-instance-admin-in-container.sh",
);

// #736: the install runs INSIDE the chat container, against Open WebUI's own
// loopback, not against OWUI_URL. OWUI_URL is caddy-owui's published port, the
// public chat origin, which now answers 404 on every path the installer writes
// to. The wrapper is the same route the demo-box deploy uses, so this job still
// exercises the deployment's own code path rather than a lookalike.
//
// Default matches this job's own boot step (`docker compose --env-file
// ../../.env.ci --profile local up -d --build` in owui-nightly.yml). Override
// with HIVE_COMPOSE_FLAGS to point the setup at a differently-composed stack.
const HIVE_COMPOSE_FLAGS =
  process.env.HIVE_COMPOSE_FLAGS ?? "--env-file ../../.env.ci --profile local";

/**
 * Resets the signed-in `page`'s own Open WebUI account password to
 * `password`, using its own admin session (see the real-fixture-login
 * comment below for why that session is already an OWUI admin).
 *
 * #712: Open WebUI's OAuth signup path (backend/open_webui/utils/oauth.py,
 * upstream v0.10.2) creates every OAuth-provisioned local account with
 * `password=get_password_hash(str(uuid.uuid4()))`, a value the comment
 * right next to it calls "Random password, not used" -- Open WebUI never
 * exposes it and no later OAuth login ever changes it. The seeded fixture
 * user here is OAuth-only (ENABLE_SIGNUP is "false", so the native signup
 * route is closed), so without this call its Open WebUI account carries a
 * password nobody knows, and a NATIVE (non-OAuth) sign-in attempt with the
 * real, freshly rotated OWUI_E2E_PASSWORD -- which 09-agent-workspace-
 * launcher.spec.ts's last test performs deliberately, to prove the launcher
 * still appears after a sign-in that never triggers a document reload --
 * always 400s at POST /api/v1/auths/signin. Not volume persistence and not
 * a seeding failure: a structural mismatch between two separate credential
 * stores that this call reconciles every run, using the admin-only
 * POST /api/v1/users/{id}/update endpoint (no old password required).
 */
async function syncOwuiLocalPassword(
  page: Page,
  owuiOrigin: string,
  password: string,
): Promise<void> {
  const session = await page.request.get(`${owuiOrigin}/api/v1/auths/`);
  if (!session.ok()) {
    throw new Error(
      `syncOwuiLocalPassword: failed to read own session: ${session.status()} ${await session.text()}`,
    );
  }
  const body: { id: string } = await session.json();
  const { id } = body;
  const updated = await page.request.post(`${owuiOrigin}/api/v1/users/${id}/update`, {
    data: { password },
  });
  if (!updated.ok()) {
    throw new Error(
      `syncOwuiLocalPassword: failed to reset local password: ${updated.status()} ${await updated.text()}`,
    );
  }
}

/**
 * Hands the already-authenticated `page`'s Open WebUI admin bearer token to
 * scripts/install-owui-jwt-forward-in-container.sh, which runs
 * scripts/install-owui-jwt-forward.py, the single implementation of the
 * install, inside the chat container (see #556, #736 and that script's
 * docstring). The script creates the
 * Filter if absent, refreshes it if the stored body has drifted from the
 * repo source, toggles it active and global only when those flags are
 * false, then re-reads the end state and fails unless it is present, active
 * AND global -- a filter only runs when it is both (see
 * open_webui.utils.filter.get_sorted_filter_ids upstream).
 */
async function installHiveJwtForwardFilter(page: Page, email: string): Promise<void> {
  // #748: nothing makes this account an Open WebUI admin by itself any more.
  // The Functions API is admin-gated, so the account is promoted inside this
  // job's own disposable chat container first. Not through
  // accounts.is_platform_admin: see this file's header for why a CI fixture
  // must not hold that. Runs after the login, because the promotion targets a
  // row that only exists once the account has signed in, and before the
  // install, because that is what needs the role.
  execFileSync(HIVE_INSTANCE_ADMIN_PROMOTER, [], {
    env: {
      ...process.env,
      OWUI_PROMOTE_EMAIL: email,
      HIVE_COMPOSE_FLAGS,
    },
    stdio: "inherit",
    // Synchronous, so a stalled `docker compose exec` blocks the event loop and
    // Playwright's own timer cannot fire: without a bound the setup project
    // hangs until the job's own limit kills it, with no diagnosis. The work is
    // one sqlite UPDATE, so a minute is already an order of magnitude of slack.
    timeout: 60_000,
  });

  // Open WebUI stores its session JWT in a `token` cookie and mirrors it into
  // localStorage. The cookie is the one the API itself reads, so it is
  // preferred; localStorage is the fallback for a build that stops setting it.
  const cookies = await page.context().cookies();
  const cookieToken = cookies.find((cookie) => cookie.name === "token")?.value;
  const token =
    cookieToken ??
    (await page.evaluate(() => window.localStorage.getItem("token")));
  if (!token) {
    throw new Error(
      "hive_jwt_forward: signed in, but no Open WebUI session token is present " +
        "in either the cookie jar or localStorage, so the Functions API cannot " +
        "be called",
    );
  }

  // Left synchronous on purpose, unlike the reseed in
  // tests/e2e/support/fixture-reset.ts and the mint in support/live-auth.ts.
  // Blocking the event loop stops Playwright's timeout timer from firing, but
  // this runs once inside a setup project rather than mid-test, so the only
  // deadline it can eat is its own, and `stdio: "inherit"` streams the
  // installer's progress into the job log live, which a buffered async child
  // would withhold until it exits. Convert it if this ever moves into a test.
  execFileSync(HIVE_JWT_FORWARD_INSTALLER, [], {
    env: {
      ...process.env,
      OWUI_ADMIN_TOKEN: token,
      HIVE_COMPOSE_FLAGS,
    },
    stdio: "inherit",
    // Same reasoning as the promotion above, with a wider bound because this
    // one copies two files into the container and makes four API calls. It has
    // never taken more than a few seconds; five minutes is a hang, not a slow
    // run, and failing on it names the stall instead of eating the job.
    timeout: 300_000,
  });
}

/**
 * Drives one full "Continue with Hive" OAuth round trip: OWUI -> Supabase
 * authorize -> /oauth/consent (web-console origin) -> optional sign-in ->
 * optional Approve -> back to an OWUI-origin URL. Shared by the bootstrap
 * login and the real fixture login below so both exercise the identical
 * redirect chain.
 */
setup("OWUI OIDC sign-in via Hive consent", async ({ page, browser }) => {
  // Run 28681926134: consent can load first, then its client-side session
  // check bounces to sign-in -- give the retry-heavy journey below real
  // time to survive that bounce instead of the 30s test default. The
  // bootstrap login below adds a second full round trip on top of the
  // original budget.
  setup.setTimeout(240_000);

  const email = process.env.OWUI_E2E_EMAIL;
  const password = process.env.OWUI_E2E_PASSWORD;
  // SUPABASE_OAUTH_CLIENT_ID/SECRET (a Supabase OAuth App registration) have
  // been provisioned as repo secrets since 2026-07-03 -- this whole journey
  // has run for real on every nightly/dispatch/labeled-PR run since. The
  // bootstrap pair below is not a stored secret; scripts/seed-owui-e2e-user.py
  // mints it fresh every run the same way it does OWUI_E2E_EMAIL/PASSWORD.
  // Keep this env check as a defensive skip only (e.g. a fork or a manual
  // Playwright run missing any of the seeded/ops-provisioned values).
  const oauthClientId = process.env.SUPABASE_OAUTH_CLIENT_ID;
  const oauthClientSecret = process.env.SUPABASE_OAUTH_CLIENT_SECRET;
  const bootstrapEmail = process.env.OWUI_BOOTSTRAP_EMAIL;
  const bootstrapPassword = process.env.OWUI_BOOTSTRAP_PASSWORD;
  if (
    !email ||
    !password ||
    !oauthClientId ||
    !oauthClientSecret ||
    !bootstrapEmail ||
    !bootstrapPassword
  ) {
    setup.skip(
      true,
      "OWUI_E2E_*/SUPABASE_OAUTH_CLIENT_*/OWUI_BOOTSTRAP_* env not set",
    );
    return;
  }

  const owuiOrigin = new URL(OWUI_URL).origin;
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

  // Bootstrap login: upstream Open WebUI auto-promoted the very first user it
  // ever saw to admin, bypassing OAUTH_ALLOWED_ROLES/OAUTH_ROLES_CLAIM
  // entirely, and the container in this job is freshly created every run, so
  // without this step the real fixture user below landed as that unconditional
  // first-user admin and never exercised the role resolution at all. Issue
  // #748 removed that promotion, so this step no longer has a promotion to
  // consume. It is kept, cheaply: it still guarantees the fixture is not the
  // instance's only account, which is what made the old promotion fire, so a
  // regression that restores it cannot hide behind this fixture.
  //
  // Runs in its own, throwaway browser context (separate cookie jar), not
  // signed out and reused in `page` -- components/oauth/consent-panel.tsx
  // only redirects an unauthenticated visitor to /auth/sign-in; if a
  // Supabase session already exists (getSession() resolves) it silently
  // completes the OAuth grant for THAT session's user with no re-prompt. A
  // shared context would therefore replay the bootstrap user's session on
  // the "real" login below instead of actually signing in the OWNER
  // fixture user -- exactly the kind of test that looks green while
  // proving nothing. A separate context makes that impossible: this
  // context is closed once the bootstrap login is confirmed, so the real
  // fixture login below starts from a page with no Supabase session at all.
  const bootstrapContext = await browser.newContext();
  const bootstrapPage = await bootstrapContext.newPage();
  await bootstrapPage.goto(OWUI_URL);
  await signInWithHive(bootstrapPage, bootstrapEmail, bootstrapPassword, owuiOrigin);
  await expect(
    bootstrapPage
      .getByRole("button", { name: /new chat/i })
      .or(bootstrapPage.getByRole("link", { name: /new chat/i }))
      .first(),
  ).toBeVisible({ timeout: 60_000 });
  await bootstrapContext.close();

  // Real fixture login (role OWNER -- see scripts/seed-owui-e2e-user.py),
  // in the setup test's own context: provably no prior Supabase session.
  await page.goto("/");
  await signInWithHive(page, email, password, owuiOrigin);
  // Run 28683831193: the chat input carries no "message" placeholder in
  // OWUI 0.9.5 -- the sidebar New Chat button is the post-login readiness
  // signal instead (present in the failure snapshot alongside the greeting).
  // Race it against the "Account Activation Pending" screen so a regression
  // of the owui_role claim / OAUTH_ALLOWED_ROLES wiring (PR #451's incident)
  // fails with a direct diagnosis instead of a bare 60s timeout.
  const activationPending = page.getByText(/activation pending/i);
  await expect(newChatButton.or(activationPending)).toBeVisible({
    timeout: 60_000,
  });
  if (await activationPending.isVisible().catch(() => false)) {
    throw new Error(
      "OWUI shows 'Account Activation Pending' for the OWNER-role e2e " +
        "fixture user -- owui_role claim / OAUTH_ALLOWED_ROLES gate " +
        "regression (see PR #451, supabase/migrations/20260726_01_owui_role_claim.sql, " +
        "and docker-compose.yml's open-webui OAUTH_ROLES_CLAIM).",
    );
  }
  await installHiveJwtForwardFilter(page, email);
  // #712: see syncOwuiLocalPassword's docstring -- without this, Open
  // WebUI's own local password for this OAuth-provisioned account never
  // matches OWUI_E2E_PASSWORD, and any native (non-OAuth) sign-in with it
  // 400s at POST /api/v1/auths/signin.
  await syncOwuiLocalPassword(page, owuiOrigin, password);
  await page.context().storageState({ path: STATE });
});
