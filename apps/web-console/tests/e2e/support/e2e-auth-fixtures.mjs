import { createHash, randomBytes } from "node:crypto";
import { pathToFileURL } from "node:url";

const FIXTURES = {
  inviterEmail: "e2e-inviter@hive-ci.test",
  verifiedPrimaryAccountId: "31aadd76-fba0-46e6-827d-e3cfef50324c",
  verifiedSecondaryAccountId: "c420b965-aed6-4bfd-a7f9-e934458b3b5a",
  invitedAccountId: "a45bec1f-e648-4811-9841-3ad28c7f34a9",
  unverifiedAccountId: "8ca58251-dfad-4e91-b2c8-b3649391871b",
  verifiedPrimaryMembershipId: "2346706c-ce29-46df-9e51-d1ee1fd98982",
  verifiedSecondaryMembershipId: "70e9e1f4-c65f-47cc-bcf7-94727188341d",
  unverifiedMembershipId: "56621485-f31b-4c32-b4fe-387d94ae1ed5",
  inviterSecondaryMembershipId: "ea48a32f-4639-4530-8790-9c97df51e940",
  inviterInvitedMembershipId: "67cd5e70-026d-4453-90da-f65bcb823e85",
  invitationId: "580df639-64b0-4a66-99f1-0cf3e293b78e",
};

function readEnv(name) {
  const value = process.env[name];
  if (!value) {
    throw new Error(`${name} is required`);
  }
  return value;
}

function hasFixtureEnv() {
  return (
    Boolean(process.env.SUPABASE_URL) &&
    Boolean(process.env.SUPABASE_SERVICE_ROLE_KEY) &&
    Boolean(process.env.E2E_VERIFIED_PASSWORD) &&
    Boolean(process.env.E2E_UNVERIFIED_PASSWORD) &&
    Boolean(process.env.E2E_INVITATION_TOKEN)
  );
}

function fixtureEmail(name, fallback) {
  return process.env[name] || fallback;
}

function authHeaders() {
  const serviceRoleKey = readEnv("SUPABASE_SERVICE_ROLE_KEY");
  return {
    Authorization: `Bearer ${serviceRoleKey}`,
    apikey: serviceRoleKey,
    "Content-Type": "application/json",
  };
}

function restUrl(path, params = {}) {
  const url = new URL(`/rest/v1/${path}`, readEnv("SUPABASE_URL"));
  for (const [key, value] of Object.entries(params)) {
    if (value !== undefined && value !== null) {
      url.searchParams.set(key, value);
    }
  }
  return url.toString();
}

function adminUrl(path) {
  return new URL(path, readEnv("SUPABASE_URL")).toString();
}

async function requestJson(url, { method = "GET", headers, body } = {}) {
  const response = await fetch(url, {
    method,
    headers,
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  const text = await response.text();
  const data = text ? JSON.parse(text) : null;
  if (!response.ok) {
    const message =
      data?.msg ??
      data?.error_description ??
      data?.error ??
      `${response.status} ${response.statusText}`;
    const error = new Error(`${method} ${url} failed: ${message}`);
    error.status = response.status;
    error.payload = data;
    throw error;
  }
  return data;
}

async function restSelect(path, params = {}) {
  return requestJson(restUrl(path, params), {
    headers: authHeaders(),
  });
}

async function restUpsert(path, body, onConflict) {
  const headers = {
    ...authHeaders(),
    Prefer: "resolution=merge-duplicates,return=minimal",
  };
  return requestJson(restUrl(path, onConflict ? { on_conflict: onConflict } : {}), {
    method: "POST",
    headers,
    body,
  });
}

async function restDelete(path, params = {}) {
  const headers = {
    ...authHeaders(),
    Prefer: "return=minimal",
  };
  return requestJson(restUrl(path, params), {
    method: "DELETE",
    headers,
  });
}

async function adminCreateUser({ email, password, emailConfirm, fullName, appMetadata }) {
  return requestJson(adminUrl("/auth/v1/admin/users"), {
    method: "POST",
    headers: authHeaders(),
    body: {
      email,
      password,
      email_confirm: emailConfirm,
      app_metadata: appMetadata,
      user_metadata: {
        full_name: fullName,
      },
    },
  });
}

async function adminUpdateUserById(userId, { password, emailConfirm, fullName, appMetadata }) {
  // Intentionally do NOT re-send `email` on update. Staging Supabase has
  // email-format validation enabled that rejects `.test` TLDs even when the
  // value is unchanged from the stored record, producing:
  //   Unable to validate email address: invalid format
  // The fixture only needs to reset the password + confirmation + metadata,
  // so the stored email is left alone.
  return requestJson(adminUrl(`/auth/v1/admin/users/${userId}`), {
    method: "PUT",
    headers: authHeaders(),
    body: {
      password,
      email_confirm: emailConfirm,
      app_metadata: appMetadata,
      user_metadata: {
        full_name: fullName,
      },
    },
  });
}

async function findUserByEmail(email) {
  for (let page = 1; page <= 500; page += 1) {
    const data = await requestJson(
      adminUrl(`/auth/v1/admin/users?page=${page}&per_page=1`),
      {
        headers: authHeaders(),
      }
    );
    const user = data?.users?.[0];
    if (!user) {
      return null;
    }
    if (user.email?.toLowerCase() === email.toLowerCase()) {
      return user;
    }
  }

  return null;
}

async function getOwnerUserId(accountId) {
  const rows = await restSelect("accounts", {
    select: "owner_user_id",
    id: `eq.${accountId}`,
  });
  return rows[0]?.owner_user_id ?? null;
}

async function ensureUser({ email, password, emailConfirm, fullName, appMetadata, accountIdHint }) {
  const hintedUserId = await getOwnerUserId(accountIdHint);
  if (hintedUserId) {
    return adminUpdateUserById(hintedUserId, {
      email,
      password,
      emailConfirm,
      fullName,
      appMetadata,
    });
  }

  try {
    return await adminCreateUser({ email, password, emailConfirm, fullName, appMetadata });
  } catch (error) {
    if (error.status !== 422 && error.status !== 400) {
      throw error;
    }

    const existing = await findUserByEmail(email);
    if (!existing?.id) {
      throw error;
    }

    return adminUpdateUserById(existing.id, {
      email,
      password,
      emailConfirm,
      fullName,
      appMetadata,
    });
  }
}

function tokenHash(rawToken) {
  return createHash("sha256").update(rawToken).digest("hex");
}

function randomPassword() {
  return randomBytes(24).toString("hex");
}

async function seedAccountsAndMemberships({ verifiedUser, unverifiedUser, inviterUser }) {
  await restUpsert(
    "accounts",
    [
      {
        id: FIXTURES.verifiedPrimaryAccountId,
        slug: "e2e-verified-workspace",
        display_name: "E2E Verified Workspace",
        account_type: "personal",
        owner_user_id: verifiedUser.id,
      },
      {
        id: FIXTURES.verifiedSecondaryAccountId,
        slug: "e2e-shared-workspace",
        display_name: "E2E Shared Workspace",
        account_type: "personal",
        owner_user_id: inviterUser.id,
      },
      {
        id: FIXTURES.invitedAccountId,
        slug: "e2e-invited-workspace",
        display_name: "E2E Invited Workspace",
        account_type: "personal",
        owner_user_id: inviterUser.id,
      },
      {
        id: FIXTURES.unverifiedAccountId,
        slug: "e2e-unverified-workspace",
        display_name: "E2E Unverified Workspace",
        account_type: "personal",
        owner_user_id: unverifiedUser.id,
      },
    ],
    "id"
  );

  const membershipDeletes = [
    { account_id: `eq.${FIXTURES.verifiedPrimaryAccountId}`, user_id: `eq.${verifiedUser.id}` },
    { account_id: `eq.${FIXTURES.verifiedSecondaryAccountId}`, user_id: `eq.${verifiedUser.id}` },
    { account_id: `eq.${FIXTURES.invitedAccountId}`, user_id: `eq.${verifiedUser.id}` },
    { account_id: `eq.${FIXTURES.unverifiedAccountId}`, user_id: `eq.${unverifiedUser.id}` },
    { account_id: `eq.${FIXTURES.verifiedSecondaryAccountId}`, user_id: `eq.${inviterUser.id}` },
    { account_id: `eq.${FIXTURES.invitedAccountId}`, user_id: `eq.${inviterUser.id}` },
  ];

  for (const params of membershipDeletes) {
    await restDelete("account_memberships", params);
  }

  await restUpsert(
    "account_memberships",
    [
      {
        id: FIXTURES.verifiedPrimaryMembershipId,
        account_id: FIXTURES.verifiedPrimaryAccountId,
        user_id: verifiedUser.id,
        role: "owner",
        status: "active",
      },
      {
        id: FIXTURES.verifiedSecondaryMembershipId,
        account_id: FIXTURES.verifiedSecondaryAccountId,
        user_id: verifiedUser.id,
        role: "member",
        status: "active",
      },
      {
        id: FIXTURES.unverifiedMembershipId,
        account_id: FIXTURES.unverifiedAccountId,
        user_id: unverifiedUser.id,
        role: "owner",
        status: "active",
      },
      {
        id: FIXTURES.inviterSecondaryMembershipId,
        account_id: FIXTURES.verifiedSecondaryAccountId,
        user_id: inviterUser.id,
        role: "owner",
        status: "active",
      },
      {
        id: FIXTURES.inviterInvitedMembershipId,
        account_id: FIXTURES.invitedAccountId,
        user_id: inviterUser.id,
        role: "owner",
        status: "active",
      },
    ],
    "id"
  );
}

async function resetProfilesAndInvitation({ verifiedUser, unverifiedUser, inviterUser }) {
  const verifiedEmail = fixtureEmail("E2E_VERIFIED_EMAIL", "e2e-verified@hive-ci.test");
  const unverifiedEmail = fixtureEmail("E2E_UNVERIFIED_EMAIL", "e2e-unverified@hive-ci.test");

  await restUpsert(
    "account_profiles",
    [
      {
        account_id: FIXTURES.verifiedPrimaryAccountId,
        owner_name: "E2E Verified Owner",
        login_email: verifiedEmail,
        country_code: null,
        state_region: null,
        profile_setup_complete: false,
      },
      {
        account_id: FIXTURES.verifiedSecondaryAccountId,
        owner_name: "E2E Shared Owner",
        login_email: verifiedEmail,
        country_code: null,
        state_region: null,
        profile_setup_complete: false,
      },
      {
        account_id: FIXTURES.invitedAccountId,
        owner_name: "E2E Inviter Owner",
        login_email: verifiedEmail,
        country_code: null,
        state_region: null,
        profile_setup_complete: false,
      },
      {
        account_id: FIXTURES.unverifiedAccountId,
        owner_name: "E2E Unverified Owner",
        login_email: unverifiedEmail,
        country_code: null,
        state_region: null,
        profile_setup_complete: false,
      },
    ],
    "account_id"
  );

  for (const accountId of [
    FIXTURES.verifiedPrimaryAccountId,
    FIXTURES.verifiedSecondaryAccountId,
    FIXTURES.invitedAccountId,
    FIXTURES.unverifiedAccountId,
  ]) {
    await restDelete("account_billing_profiles", {
      account_id: `eq.${accountId}`,
    });
  }

  await restDelete("account_invitations", {
    account_id: `eq.${FIXTURES.invitedAccountId}`,
    email: `eq.${verifiedEmail}`,
  });

  await restUpsert(
    "account_invitations",
    [
      {
        id: FIXTURES.invitationId,
        account_id: FIXTURES.invitedAccountId,
        email: verifiedEmail,
        role: "member",
        token_hash: tokenHash(readEnv("E2E_INVITATION_TOKEN")),
        expires_at: "2099-01-01T00:00:00Z",
        accepted_at: null,
        invited_by_user_id: inviterUser.id,
      },
    ],
    "id"
  );

  const userAudits = [
    {
      accountId: FIXTURES.verifiedPrimaryAccountId,
      userId: verifiedUser.id,
      role: "owner",
    },
    {
      accountId: FIXTURES.verifiedSecondaryAccountId,
      userId: verifiedUser.id,
      role: "member",
    },
    {
      accountId: FIXTURES.unverifiedAccountId,
      userId: unverifiedUser.id,
      role: "owner",
    },
  ];

  for (const audit of userAudits) {
    const rows = await restSelect("account_memberships", {
      select: "id,role,status",
      account_id: `eq.${audit.accountId}`,
      user_id: `eq.${audit.userId}`,
    });
    if (rows.length !== 1 || rows[0].role !== audit.role || rows[0].status !== "active") {
      throw new Error(
        `membership baseline missing for account ${audit.accountId} and user ${audit.userId}`
      );
    }
  }
}

export async function prepareE2EAuthFixtures() {
  if (!hasFixtureEnv()) {
    return;
  }

  const verifiedEmail = fixtureEmail("E2E_VERIFIED_EMAIL", "e2e-verified@hive-ci.test");
  const unverifiedEmail = fixtureEmail("E2E_UNVERIFIED_EMAIL", "e2e-unverified@hive-ci.test");
  const verifiedPassword = readEnv("E2E_VERIFIED_PASSWORD");
  const unverifiedPassword = readEnv("E2E_UNVERIFIED_PASSWORD");

  const [verifiedUser, unverifiedUser, inviterUser] = await Promise.all([
    ensureUser({
      email: verifiedEmail,
      password: verifiedPassword,
      emailConfirm: true,
      appMetadata: { hive_email_verified: true },
      fullName: "E2E Verified Owner",
      accountIdHint: FIXTURES.verifiedPrimaryAccountId,
    }),
    ensureUser({
      email: unverifiedEmail,
      password: unverifiedPassword,
      emailConfirm: true,
      appMetadata: { hive_email_verified: false },
      fullName: "E2E Unverified Owner",
      accountIdHint: FIXTURES.unverifiedAccountId,
    }),
    ensureUser({
      email: FIXTURES.inviterEmail,
      password: randomPassword(),
      emailConfirm: true,
      appMetadata: { hive_email_verified: true },
      fullName: "E2E Inviter Owner",
      accountIdHint: FIXTURES.verifiedSecondaryAccountId,
    }),
  ]);

  await seedAccountsAndMemberships({ verifiedUser, unverifiedUser, inviterUser });
  await resetProfilesAndInvitation({ verifiedUser, unverifiedUser, inviterUser });

  return {
    verifiedEmail,
    unverifiedEmail,
    verifiedUserId: verifiedUser.id,
    unverifiedUserId: unverifiedUser.id,
    inviterUserId: inviterUser.id,
    verifiedPrimaryAccountId: FIXTURES.verifiedPrimaryAccountId,
    verifiedSecondaryAccountId: FIXTURES.verifiedSecondaryAccountId,
    invitedAccountId: FIXTURES.invitedAccountId,
    unverifiedAccountId: FIXTURES.unverifiedAccountId,
  };
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  prepareE2EAuthFixtures()
    .then((summary) => {
      if (summary) {
        console.log(JSON.stringify(summary, null, 2));
      }
    })
    .catch((error) => {
      console.error(error instanceof Error ? error.message : error);
      process.exitCode = 1;
    });
}
