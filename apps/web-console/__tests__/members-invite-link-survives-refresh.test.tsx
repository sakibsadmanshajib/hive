import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

// THE REGRESSION TEST FOR THE WORST FUNCTIONAL FAILURE THIS FEATURE CAN HAVE.
//
// The acceptance token exists for exactly one response and the database keeps
// only its hash, so the link rendered after issuing an invitation is the only
// copy that will ever exist. Issuing also calls router.refresh(), so the
// members table re-renders with the new invitation in it.
//
// If the link lives inside a table row, that refresh can destroy it: DataTable
// keys each row on its row key, and a row whose key changes is a different row
// to React, so it remounts and its state goes with it. The user would see
// success, see a link, and watch it vanish with no explanation. That is worse
// than the silent failure this whole change exists to fix, because it looks
// like the feature working right up until it does not.
//
// So the link is held above the table, and this test is what says so. It fails
// if anybody moves that state back into a row, and it fails on the real
// component tree rather than on reasoning about keys.

const mockRedirect = vi.fn();
const mockRefresh = vi.fn();
const mockGetUser = vi.fn();
const mockGetSession = vi.fn();
const mockCreateClient = vi.fn(() => ({
  auth: { getUser: mockGetUser, getSession: mockGetSession },
}));
const mockGetViewer = vi.fn();
const mockGetMembers = vi.fn();
const mockGetAccountProfile = vi.fn();

// Only the navigation calls this test drives are replaced.
// unstable_rethrow() stays real: the console's reads call it first in
// every catch so a framework throw is never classified as a data
// failure, and a stubbed one would pass whether or not that holds.
vi.mock("next/navigation", async (importOriginal) => {
  const actual = await importOriginal<typeof import("next/navigation")>();
  return {
    ...actual,
  redirect: mockRedirect,
  useRouter: () => ({ refresh: mockRefresh }),
  };
});

vi.mock("next/headers", () => ({
  cookies: vi.fn(async () => ({
    get: vi.fn(() => undefined),
    getAll: vi.fn(() => []),
  })),
}));

vi.mock("../lib/supabase/server", () => ({ createClient: mockCreateClient }));

vi.mock("../lib/control-plane/client", () => ({
  getViewer: mockGetViewer,
  getMembers: mockGetMembers,
  getAccountProfile: mockGetAccountProfile,
  ControlPlaneError: class ControlPlaneError extends Error {
    status: number;
    constructor(status: number, message: string) {
      super(message);
      this.status = status;
    }
  },
}));

vi.mock("@/components/app-shell/console-shell", () => ({
  ConsoleShell: ({ children }: { children: React.ReactNode }) => (
    <div>{children}</div>
  ),
}));

const OWNER_ID = "11111111-1111-4111-8111-111111111111";
const LINK =
  "https://console.example.test/invitations/accept?token=REDACTED-NOT-A-REAL-TOKEN";

function roster(invitations: unknown[]) {
  return {
    invitations,
    members: [
      {
        user_id: OWNER_ID,
        email: "owner@example.test",
        role: "owner",
        status: "active",
      },
    ],
  };
}

function invitation(id: string) {
  return {
    id,
    email: "invitee@example.test",
    role: "member",
    status: "pending",
    expires_at: "2026-09-01T09:30:00Z",
    created_at: "2026-08-29T09:30:00Z",
  };
}

// Reaches the form without a non-null assertion, so a missing form is a named
// failure rather than a null dereference three lines later.
function inviteForm(): HTMLFormElement {
  const submit = screen.getByRole("button", { name: /create invitation/i });
  const form = submit.closest("form");
  if (form === null) {
    throw new Error("the invite submit button is not inside a form");
  }
  return form;
}

async function renderPage() {
  const mod = await import("../app/console/members/page");
  return await mod.default({ searchParams: Promise.resolve({}) });
}

beforeEach(() => {
  vi.clearAllMocks();
  mockGetUser.mockResolvedValue({
    data: { user: { id: OWNER_ID, email: "owner@example.test" } },
    error: null,
  });
  mockGetSession.mockResolvedValue({
    data: { session: { access_token: "session-token" } },
  });
  mockGetAccountProfile.mockResolvedValue({ owner_name: "Ada Owner" });
  mockGetViewer.mockResolvedValue({
    user: { id: OWNER_ID, email: "owner@example.test", email_verified: true },
    current_account: {
      id: "a1",
      slug: "acme",
      display_name: "Acme",
      account_type: "business",
      role: "owner",
    },
    memberships: [],
    permissions: ["members.invite", "members.manage"],
  });
  vi.stubGlobal(
    "fetch",
    vi.fn(
      async () =>
        new Response(
          JSON.stringify({
            email: "invitee@example.test",
            role: "member",
            delivered: false,
            delivery: "not_configured",
            link: LINK,
          }),
          { status: 201, headers: { "Content-Type": "application/json" } },
        ),
    ),
  );
});

describe("the one-time invitation link", () => {
  it("survives the table re-rendering underneath it, even with a different row identity", async () => {
    mockGetMembers.mockResolvedValue(roster([]));
    const { rerender } = render(await renderPage());

    fireEvent.change(screen.getByLabelText(/email/i), {
      target: { value: "invitee@example.test" },
    });
    fireEvent.submit(inviteForm());

    expect(await screen.findByDisplayValue(LINK)).toBeTruthy();
    await waitFor(() => expect(mockRefresh).toHaveBeenCalled());

    // What router.refresh() does: the server component runs again and the table
    // comes back carrying the new invitation. The id is deliberately different
    // from anything rendered before, which is the case that used to remount and
    // wipe the link.
    mockGetMembers.mockResolvedValue(roster([invitation("row-id-that-changed")]));
    rerender(await renderPage());

    expect(screen.getByText("invitee@example.test")).toBeTruthy();
    expect(await screen.findByDisplayValue(LINK)).toBeTruthy();
  });

  // The reported failure, exactly. Resending is triggered from inside a table
  // row, so this is the path where a row remount could take the link with it.
  it("survives a refresh when it was issued from a row action", async () => {
    mockGetMembers.mockResolvedValue(roster([invitation("row-before")]));
    const { rerender } = render(await renderPage());

    fireEvent.click(screen.getByRole("button", { name: /new link/i }));

    expect(await screen.findByDisplayValue(LINK)).toBeTruthy();
    await waitFor(() => expect(mockRefresh).toHaveBeenCalled());

    // The refresh brings the row back with a different identity, which is what
    // remounts it. The link is not in the row, so it does not care.
    mockGetMembers.mockResolvedValue(roster([invitation("row-after")]));
    rerender(await renderPage());

    expect(await screen.findByDisplayValue(LINK)).toBeTruthy();
  });

  it("is issued once per submission even when the control is hammered", async () => {
    mockGetMembers.mockResolvedValue(roster([]));
    render(await renderPage());

    fireEvent.change(screen.getByLabelText(/email/i), {
      target: { value: "invitee@example.test" },
    });
    const form = inviteForm();
    // Three submissions inside one React batch. Without the in-flight guard the
    // extra ones mint fresh tokens, and because issuing supersedes, the link the
    // user is looking at would already be dead.
    fireEvent.submit(form);
    fireEvent.submit(form);
    fireEvent.submit(form);

    await screen.findByDisplayValue(LINK);
    expect(vi.mocked(fetch)).toHaveBeenCalledTimes(1);
  });
});
