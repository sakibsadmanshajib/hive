// Supabase Edge Function: e2e-fixtures
//
// Server-side seed + reset of E2E test users, accounts, memberships,
// profiles, and a single pending invitation. Replaces the per-test
// admin-API round-tripping that used to live in
// `apps/web-console/tests/e2e/support/e2e-auth-fixtures.mjs` and keeps
// the service-role key out of CI runners entirely — Playwright only
// needs the edge function's shared `E2E_FIXTURE_SECRET`.
//
// Deploy:
//   supabase functions deploy e2e-fixtures \
//     --project-ref <ref> --no-verify-jwt
//   supabase secrets set E2E_FIXTURE_SECRET=<long-random-string>
//
// Caller contract:
//   POST /functions/v1/e2e-fixtures
//   Headers: X-E2E-Secret: <E2E_FIXTURE_SECRET>
//   Body:    { "action": "reset" }
//   200:     { verifiedEmail, unverifiedEmail, verifiedPassword,
//              unverifiedPassword, invitationToken, verifiedUserId,
//              unverifiedUserId, inviterUserId,
//              verifiedPrimaryAccountId, verifiedSecondaryAccountId,
//              invitedAccountId, unverifiedAccountId }
//
// Every call produces the same deterministic state: the three test
// users exist and are password-reset, one invitation is pending for
// the verified user, and all profile/billing mutations from prior
// runs are cleared.

// deno-lint-ignore-file no-explicit-any
import { createClient } from "npm:@supabase/supabase-js@2";

const FIXED = {
  inviterEmail: "e2e-inviter@scubed.com.bd",
  verifiedPrimaryAccountId: "31aadd76-fba0-46e6-827d-e3cfef50324c",
  verifiedSecondaryAccountId: "c420b965-aed6-4bfd-a7f9-e934458b3b5a",
  invitedAccountId: "a45bec1f-e648-4811-9841-3ad28c7f34a9",
  unverifiedAccountId: "8ca58251-dfad-4e91-b2c8-b3649391871b",
  invitationId: "580df639-64b0-4a66-99f1-0cf3e293b78e",
};

const DEFAULTS = {
  verifiedEmail: "e2e-verified@scubed.com.bd",
  unverifiedEmail: "e2e-unverified@scubed.com.bd",
  verifiedPassword: "E2eFixture-Verified#2026",
  unverifiedPassword: "E2eFixture-Unverified#2026",
  invitationToken: "e2e-invitation-token-2026-fixture",
};

function jsonResponse(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

async function sha256Hex(input: string): Promise<string> {
  const bytes = new TextEncoder().encode(input);
  const digest = await crypto.subtle.digest("SHA-256", bytes);
  return Array.from(new Uint8Array(digest))
    .map((b) => b.toString(16).padStart(2, "0"))
    .join("");
}

function randomPassword(): string {
  const bytes = new Uint8Array(24);
  crypto.getRandomValues(bytes);
  return Array.from(bytes)
    .map((b) => b.toString(16).padStart(2, "0"))
    .join("");
}

async function ensureUser(
  admin: any,
  opts: {
    email: string;
    password: string;
    emailConfirm: boolean;
    fullName: string;
    appMetadata: Record<string, unknown>;
    accountIdHint: string;
  },
) {
  const { data: ownerRow } = await admin
    .from("accounts")
    .select("owner_user_id")
    .eq("id", opts.accountIdHint)
    .maybeSingle();

  if (ownerRow?.owner_user_id) {
    const { data, error } = await admin.auth.admin.updateUserById(
      ownerRow.owner_user_id,
      {
        email: opts.email,
        password: opts.password,
        email_confirm: opts.emailConfirm,
        app_metadata: opts.appMetadata,
        user_metadata: { full_name: opts.fullName },
      },
    );
    if (error) throw new Error(`updateUserById failed: ${error.message}`);
    return data.user;
  }

  const { data, error } = await admin.auth.admin.createUser({
    email: opts.email,
    password: opts.password,
    email_confirm: opts.emailConfirm,
    app_metadata: opts.appMetadata,
    user_metadata: { full_name: opts.fullName },
  });
  if (error) {
    if (error.status === 422 || error.status === 400) {
      const { data: list, error: listErr } = await admin.auth.admin.listUsers({
        page: 1,
        perPage: 1000,
      });
      if (listErr) throw new Error(`listUsers failed: ${listErr.message}`);
      const existing = list.users.find(
        (u: any) => u.email?.toLowerCase() === opts.email.toLowerCase(),
      );
      if (!existing) throw new Error(error.message);
      const { data: upd, error: updErr } =
        await admin.auth.admin.updateUserById(existing.id, {
          email: opts.email,
          password: opts.password,
          email_confirm: opts.emailConfirm,
          app_metadata: opts.appMetadata,
          user_metadata: { full_name: opts.fullName },
        });
      if (updErr) throw new Error(`updateUserById failed: ${updErr.message}`);
      return upd.user;
    }
    throw new Error(`createUser failed: ${error.message}`);
  }
  return data.user;
}

async function seedAccountsAndMemberships(
  admin: any,
  users: { verifiedUser: any; unverifiedUser: any; inviterUser: any },
) {
  const { verifiedUser, unverifiedUser, inviterUser } = users;

  const { error: accErr } = await admin.from("accounts").upsert(
    [
      {
        id: FIXED.verifiedPrimaryAccountId,
        slug: "e2e-verified-workspace",
        display_name: "E2E Verified Workspace",
        account_type: "personal",
        owner_user_id: verifiedUser.id,
      },
      {
        id: FIXED.verifiedSecondaryAccountId,
        slug: "e2e-shared-workspace",
        display_name: "E2E Shared Workspace",
        account_type: "personal",
        owner_user_id: inviterUser.id,
      },
      {
        id: FIXED.invitedAccountId,
        slug: "e2e-invited-workspace",
        display_name: "E2E Invited Workspace",
        account_type: "personal",
        owner_user_id: inviterUser.id,
      },
      {
        id: FIXED.unverifiedAccountId,
        slug: "e2e-unverified-workspace",
        display_name: "E2E Unverified Workspace",
        account_type: "personal",
        owner_user_id: unverifiedUser.id,
      },
    ],
    { onConflict: "id" },
  );
  if (accErr) throw new Error(`accounts upsert failed: ${accErr.message}`);

  const pairsToClear: Array<[string, string]> = [
    [FIXED.verifiedPrimaryAccountId, verifiedUser.id],
    [FIXED.verifiedSecondaryAccountId, verifiedUser.id],
    [FIXED.invitedAccountId, verifiedUser.id],
    [FIXED.unverifiedAccountId, unverifiedUser.id],
    [FIXED.verifiedSecondaryAccountId, inviterUser.id],
    [FIXED.invitedAccountId, inviterUser.id],
  ];

  for (const [accountId, userId] of pairsToClear) {
    const { error } = await admin
      .from("account_memberships")
      .delete()
      .eq("account_id", accountId)
      .eq("user_id", userId);
    if (error) {
      throw new Error(
        `membership delete failed (${accountId}/${userId}): ${error.message}`,
      );
    }
  }

  const { error: memErr } = await admin
    .from("account_memberships")
    .upsert(
      [
        {
          account_id: FIXED.verifiedPrimaryAccountId,
          user_id: verifiedUser.id,
          role: "owner",
          status: "active",
        },
        {
          account_id: FIXED.verifiedSecondaryAccountId,
          user_id: verifiedUser.id,
          role: "member",
          status: "active",
        },
        {
          account_id: FIXED.unverifiedAccountId,
          user_id: unverifiedUser.id,
          role: "owner",
          status: "active",
        },
        {
          account_id: FIXED.verifiedSecondaryAccountId,
          user_id: inviterUser.id,
          role: "owner",
          status: "active",
        },
        {
          account_id: FIXED.invitedAccountId,
          user_id: inviterUser.id,
          role: "owner",
          status: "active",
        },
      ],
      { onConflict: "account_id,user_id" },
    );
  if (memErr) throw new Error(`memberships upsert failed: ${memErr.message}`);
}

async function resetProfilesAndInvitation(
  admin: any,
  users: { verifiedUser: any; unverifiedUser: any; inviterUser: any },
  invitationTokenHash: string,
  invitationEmail: string,
  unverifiedEmail: string,
) {
  const { inviterUser } = users;

  const { error: profErr } = await admin.from("account_profiles").upsert(
    [
      {
        account_id: FIXED.verifiedPrimaryAccountId,
        owner_name: "E2E Verified Owner",
        login_email: invitationEmail,
        country_code: null,
        state_region: null,
        profile_setup_complete: false,
      },
      {
        account_id: FIXED.verifiedSecondaryAccountId,
        owner_name: "E2E Shared Owner",
        login_email: invitationEmail,
        country_code: null,
        state_region: null,
        profile_setup_complete: false,
      },
      {
        account_id: FIXED.invitedAccountId,
        owner_name: "E2E Inviter Owner",
        login_email: invitationEmail,
        country_code: null,
        state_region: null,
        profile_setup_complete: false,
      },
      {
        account_id: FIXED.unverifiedAccountId,
        owner_name: "E2E Unverified Owner",
        login_email: unverifiedEmail,
        country_code: null,
        state_region: null,
        profile_setup_complete: false,
      },
    ],
    { onConflict: "account_id" },
  );
  if (profErr) throw new Error(`profiles upsert failed: ${profErr.message}`);

  const accountIds = [
    FIXED.verifiedPrimaryAccountId,
    FIXED.verifiedSecondaryAccountId,
    FIXED.invitedAccountId,
    FIXED.unverifiedAccountId,
  ];
  for (const accountId of accountIds) {
    const { error } = await admin
      .from("account_billing_profiles")
      .delete()
      .eq("account_id", accountId);
    if (error) {
      throw new Error(
        `billing profile delete failed (${accountId}): ${error.message}`,
      );
    }
  }

  const { error: delInvErr } = await admin
    .from("account_invitations")
    .delete()
    .eq("account_id", FIXED.invitedAccountId)
    .eq("email", invitationEmail);
  if (delInvErr) {
    throw new Error(`invitation delete failed: ${delInvErr.message}`);
  }

  const { error: upInvErr } = await admin
    .from("account_invitations")
    .upsert(
      [
        {
          id: FIXED.invitationId,
          account_id: FIXED.invitedAccountId,
          email: invitationEmail,
          role: "member",
          token_hash: invitationTokenHash,
          expires_at: "2099-01-01T00:00:00Z",
          accepted_at: null,
          invited_by_user_id: inviterUser.id,
        },
      ],
      { onConflict: "id" },
    );
  if (upInvErr) throw new Error(`invitation upsert failed: ${upInvErr.message}`);
}

Deno.serve(async (req) => {
  if (req.method !== "POST") {
    return jsonResponse({ error: "method not allowed" }, 405);
  }

  // Auth: accept EITHER the dedicated E2E_FIXTURE_SECRET (when set) OR the
  // auto-injected SUPABASE_SERVICE_ROLE_KEY. The service role key is what
  // every caller in CI / local already has, so accepting it removes the
  // separate secret-setup step while keeping the endpoint locked to the same
  // blast radius (root DB access).
  const acceptedSecrets = [
    Deno.env.get("E2E_FIXTURE_SECRET"),
    Deno.env.get("SUPABASE_SERVICE_ROLE_KEY"),
  ].filter((v): v is string => !!v);
  if (acceptedSecrets.length === 0) {
    return jsonResponse(
      { error: "E2E_FIXTURE_SECRET / SUPABASE_SERVICE_ROLE_KEY not configured" },
      500,
    );
  }
  const provided = req.headers.get("X-E2E-Secret");
  if (!provided || !acceptedSecrets.includes(provided)) {
    return jsonResponse({ error: "unauthorized" }, 401);
  }

  let body: { action?: string } = {};
  try {
    body = await req.json();
  } catch {
    // empty body allowed — default to reset
  }
  const action = body.action ?? "reset";
  if (action !== "reset") {
    return jsonResponse({ error: `unknown action: ${action}` }, 400);
  }

  const supabaseUrl = Deno.env.get("SUPABASE_URL");
  const serviceRoleKey = Deno.env.get("SUPABASE_SERVICE_ROLE_KEY");
  if (!supabaseUrl || !serviceRoleKey) {
    return jsonResponse(
      { error: "SUPABASE_URL / SUPABASE_SERVICE_ROLE_KEY missing" },
      500,
    );
  }

  const admin = createClient(supabaseUrl, serviceRoleKey, {
    auth: { persistSession: false, autoRefreshToken: false },
  });

  const verifiedEmail =
    Deno.env.get("E2E_DEFAULT_VERIFIED_EMAIL") ?? DEFAULTS.verifiedEmail;
  const unverifiedEmail =
    Deno.env.get("E2E_DEFAULT_UNVERIFIED_EMAIL") ?? DEFAULTS.unverifiedEmail;
  const verifiedPassword =
    Deno.env.get("E2E_DEFAULT_VERIFIED_PASSWORD") ?? DEFAULTS.verifiedPassword;
  const unverifiedPassword =
    Deno.env.get("E2E_DEFAULT_UNVERIFIED_PASSWORD") ??
    DEFAULTS.unverifiedPassword;
  const invitationToken =
    Deno.env.get("E2E_DEFAULT_INVITATION_TOKEN") ?? DEFAULTS.invitationToken;

  try {
    const [verifiedUser, unverifiedUser, inviterUser] = await Promise.all([
      ensureUser(admin, {
        email: verifiedEmail,
        password: verifiedPassword,
        emailConfirm: true,
        appMetadata: { hive_email_verified: true },
        fullName: "E2E Verified Owner",
        accountIdHint: FIXED.verifiedPrimaryAccountId,
      }),
      ensureUser(admin, {
        email: unverifiedEmail,
        password: unverifiedPassword,
        emailConfirm: true,
        appMetadata: { hive_email_verified: false },
        fullName: "E2E Unverified Owner",
        accountIdHint: FIXED.unverifiedAccountId,
      }),
      ensureUser(admin, {
        email: FIXED.inviterEmail,
        password: randomPassword(),
        emailConfirm: true,
        appMetadata: { hive_email_verified: true },
        fullName: "E2E Inviter Owner",
        accountIdHint: FIXED.verifiedSecondaryAccountId,
      }),
    ]);

    await seedAccountsAndMemberships(admin, {
      verifiedUser,
      unverifiedUser,
      inviterUser,
    });
    await resetProfilesAndInvitation(
      admin,
      { verifiedUser, unverifiedUser, inviterUser },
      await sha256Hex(invitationToken),
      verifiedEmail,
      unverifiedEmail,
    );

    return jsonResponse({
      verifiedEmail,
      unverifiedEmail,
      verifiedPassword,
      unverifiedPassword,
      invitationToken,
      verifiedUserId: verifiedUser.id,
      unverifiedUserId: unverifiedUser.id,
      inviterUserId: inviterUser.id,
      verifiedPrimaryAccountId: FIXED.verifiedPrimaryAccountId,
      verifiedSecondaryAccountId: FIXED.verifiedSecondaryAccountId,
      invitedAccountId: FIXED.invitedAccountId,
      unverifiedAccountId: FIXED.unverifiedAccountId,
    });
  } catch (err) {
    const message = err instanceof Error ? err.message : String(err);
    return jsonResponse({ error: message }, 500);
  }
});
