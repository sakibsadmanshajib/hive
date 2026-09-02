/**
 * Issue #1660. The console decided its workspace-administration surfaces from
 * `current_account.role`, which traces to public.account_memberships, while the
 * control-plane gates those same surfaces on public.tenant_users. A personal
 * tenant's sole owner is 'owner' in the first table and 'MEMBER' in the second
 * by design, so the console rendered a page the backend then refused.
 *
 * GET /api/v1/viewer now carries `workspace_admin`, resolved from the table the
 * gate actually reads. These cases pin the console's decoding of it, including
 * the fail-closed default: a control-plane that predates the field must leave
 * the console denying rather than falling back to the account role it used to
 * conflate with tenant authority.
 */
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const mockGetUser = vi.fn();
const mockGetSession = vi.fn();

vi.mock("next/headers", () => ({
  cookies: vi.fn(async () => ({
    get: vi.fn(() => undefined),
    getAll: vi.fn(() => []),
  })),
}));

vi.mock("../lib/supabase/server", () => ({
  createClient: vi.fn(() => ({
    auth: { getUser: mockGetUser, getSession: mockGetSession },
  })),
}));

const BASE_URL = "http://control-plane.internal:8081";

interface ViewerPayload {
  user: { id: string; email: string; email_verified: boolean };
  current_account: {
    id: string;
    display_name: string;
    account_type: string;
    role: string;
  };
  memberships: never[];
  permissions: string[];
  workspace_admin?: boolean;
}

function payload(workspaceAdmin?: boolean): ViewerPayload {
  const body: ViewerPayload = {
    user: { id: "u1", email: "solo-owner@example.test", email_verified: true },
    current_account: {
      id: "a1",
      display_name: "Personal workspace",
      account_type: "personal",
      role: "owner",
    },
    memberships: [],
    permissions: ["workspace.settings"],
  };
  if (workspaceAdmin !== undefined) {
    body.workspace_admin = workspaceAdmin;
  }
  return body;
}

let nextResponse: Response = new Response("", { status: 200 });

describe("viewer workspace_admin signal", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    process.env.CONTROL_PLANE_BASE_URL = BASE_URL;
    mockGetUser.mockResolvedValue({ data: { user: { id: "u1" } }, error: null });
    mockGetSession.mockResolvedValue({
      data: { session: { access_token: "ACCESS_TOKEN_PLACEHOLDER" } },
    });
    vi.stubGlobal("fetch", () => Promise.resolve(nextResponse));
  });

  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it("carries workspace_admin true through to the viewer", async () => {
    nextResponse = new Response(JSON.stringify(payload(true)), { status: 200 });
    const { getViewer } = await import("../lib/control-plane/client");

    const viewer = await getViewer();

    expect(viewer.workspace_admin).toBe(true);
  });

  it("keeps an account-role owner out of workspace administration when the tenant says otherwise", async () => {
    nextResponse = new Response(JSON.stringify(payload(false)), { status: 200 });
    const { getViewer } = await import("../lib/control-plane/client");

    const viewer = await getViewer();

    expect(viewer.current_account.role).toBe("owner");
    expect(viewer.workspace_admin).toBe(false);
  });

  it("fails closed when the field is absent", async () => {
    nextResponse = new Response(JSON.stringify(payload()), { status: 200 });
    const { getViewer } = await import("../lib/control-plane/client");

    const viewer = await getViewer();

    expect(viewer.workspace_admin).toBe(false);
  });
});
