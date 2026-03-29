import { cookies } from "next/headers";
import { createClient } from "@/lib/supabase/server";

export interface ViewerAccount {
  id: string;
  display_name: string;
  slug: string;
  account_type: string;
  role: string;
}

export interface ViewerMembership {
  account_id: string;
  account_display_name: string;
  account_slug: string;
  display_name: string;
  role: string;
  status: string;
}

export interface ViewerUser {
  id: string;
  email: string;
  email_verified: boolean;
}

export interface ViewerGates {
  can_invite_members: boolean;
  can_manage_api_keys: boolean;
}

export interface Viewer {
  user: ViewerUser;
  current_account: ViewerAccount;
  memberships: ViewerMembership[];
  gates: ViewerGates;
}

export interface AccountProfile {
  owner_name: string;
  login_email: string;
  display_name: string;
  account_type: string;
  country_code: string;
  state_region: string;
  profile_setup_complete: boolean;
}

export interface UpdateAccountProfileInput {
  ownerName: string;
  loginEmail: string;
  accountName: string;
  accountType: string;
  countryCode: string;
  stateRegion: string;
}

interface ViewerResponse {
  user: ViewerUser;
  current_account: {
    id: string;
    display_name: string;
    account_type: string;
    role: string;
  };
  memberships: Array<{
    account_id: string;
    display_name: string;
    role: string;
    status: string;
  }>;
  gates: ViewerGates;
}

async function getRequestContext() {
  const cookieStore = await cookies();
  const supabase = createClient(cookieStore);

  const {
    data: { session },
  } = await supabase.auth.getSession();

  if (!session) {
    throw new Error("No active session");
  }

  const baseUrl = process.env.CONTROL_PLANE_BASE_URL;
  if (!baseUrl) {
    throw new Error("CONTROL_PLANE_BASE_URL is not configured");
  }

  const headers: Record<string, string> = {
    Authorization: `Bearer ${session.access_token}`,
    "Content-Type": "application/json",
  };

  const accountId = cookieStore.get("hive_account_id")?.value;
  if (accountId) {
    headers["X-Hive-Account-ID"] = accountId;
  }

  return { baseUrl, headers };
}

async function readResponseError(response: Response, fallback: string) {
  const body = (await response.json().catch(() => null)) as
    | { error?: string }
    | null;

  return body?.error ?? `${fallback}: ${response.status}`;
}

export async function getViewer(): Promise<Viewer> {
  const { baseUrl, headers } = await getRequestContext();

  const response = await fetch(`${baseUrl}/api/v1/viewer`, {
    headers,
    cache: "no-store",
  });

  if (!response.ok) {
    throw new Error(await readResponseError(response, "Failed to fetch viewer"));
  }

  const rawViewer = (await response.json()) as ViewerResponse;

  return {
    user: rawViewer.user,
    current_account: {
      id: rawViewer.current_account.id,
      display_name: rawViewer.current_account.display_name,
      slug: "",
      account_type: rawViewer.current_account.account_type,
      role: rawViewer.current_account.role,
    },
    memberships: rawViewer.memberships.map((membership) => ({
      account_id: membership.account_id,
      account_display_name: membership.display_name,
      account_slug: "",
      display_name: membership.display_name,
      role: membership.role,
      status: membership.status,
    })),
    gates: rawViewer.gates,
  };
}

export async function getAccountProfile(): Promise<AccountProfile> {
  const { baseUrl, headers } = await getRequestContext();
  const response = await fetch(`${baseUrl}/api/v1/accounts/current/profile`, {
    headers,
    cache: "no-store",
  });

  if (!response.ok) {
    throw new Error(await readResponseError(response, "Failed to fetch account profile"));
  }

  return response.json() as Promise<AccountProfile>;
}

export async function updateAccountProfile(
  input: UpdateAccountProfileInput
): Promise<AccountProfile> {
  const { baseUrl, headers } = await getRequestContext();
  const response = await fetch(`${baseUrl}/api/v1/accounts/current/profile`, {
    method: "PUT",
    headers,
    cache: "no-store",
    body: JSON.stringify({
      owner_name: input.ownerName,
      login_email: input.loginEmail,
      display_name: input.accountName,
      account_type: input.accountType,
      country_code: input.countryCode,
      state_region: input.stateRegion,
    }),
  });

  if (!response.ok) {
    throw new Error(await readResponseError(response, "Failed to update account profile"));
  }

  return response.json() as Promise<AccountProfile>;
}

export async function getMembers(accessToken: string): Promise<unknown[]> {
  const baseUrl = process.env.CONTROL_PLANE_BASE_URL;
  if (!baseUrl) {
    throw new Error("CONTROL_PLANE_BASE_URL is not configured");
  }

  const response = await fetch(
    `${baseUrl}/api/v1/accounts/current/members`,
    {
      headers: {
        Authorization: `Bearer ${accessToken}`,
        "Content-Type": "application/json",
      },
      cache: "no-store",
    }
  );

  if (!response.ok) {
    throw new Error(`Failed to fetch members: ${response.status}`);
  }

  return response.json() as Promise<unknown[]>;
}
