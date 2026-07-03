/**
 * TDD: the OWUI OIDC consent page (issue #269).
 *
 * Contract exercised here (per Supabase's OAuth 2.1 server docs):
 * - Missing authorization_id -> error state, no Supabase calls.
 * - No session -> client-side redirect to /auth/sign-in?next=<consent url>.
 * - Session present, consent needed -> renders client name + scopes with
 *   Approve/Deny actions.
 * - Session present, already consented (server returns only redirect_url)
 *   -> immediate hand-off, no consent UI shown.
 * - Approve/deny call the matching Supabase method and follow redirect_url.
 * - Errors from any Supabase call surface as an alert instead of a redirect.
 *
 * Strict TS: no `as`/`any`/`unknown` casts. Mocks are built structurally
 * against the real @supabase/supabase-js response shapes.
 */
import { beforeEach, describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";

const mockGetSession = vi.fn();
const mockGetAuthorizationDetails = vi.fn();
const mockApproveAuthorization = vi.fn();
const mockDenyAuthorization = vi.fn();

vi.mock("@/lib/supabase/browser", () => ({
  createClient: () => ({
    auth: {
      getSession: mockGetSession,
      oauth: {
        getAuthorizationDetails: mockGetAuthorizationDetails,
        approveAuthorization: mockApproveAuthorization,
        denyAuthorization: mockDenyAuthorization,
      },
    },
  }),
}));

// jsdom's window.location.assign is non-configurable/non-writable, so it
// cannot be spied on directly. ConsentPanel calls the small navigate()
// wrapper instead, which we mock here. The vi.fn() must live inside the
// factory (not a hoisted-above const) or vitest's hoisting hits a TDZ error.
vi.mock("@/lib/navigate", () => ({
  navigate: vi.fn(),
}));

import { ConsentPanel, buildSignInRedirect } from "./consent-panel";
import { navigate } from "@/lib/navigate";

const mockNavigate = vi.mocked(navigate);

const AUTH_ID = "auth-req-abc123";

const AUTHENTICATED_SESSION = {
  data: { session: { access_token: "session-token" } },
  error: null,
};

const CONSENT_NEEDED_DETAILS = {
  data: {
    authorization_id: AUTH_ID,
    redirect_uri: "https://owui.example.com/oauth/oidc/callback",
    client: {
      id: "client-1",
      name: "Hive Chat",
      uri: "https://owui.example.com",
      logo_uri: "",
    },
    user: { id: "user-1", email: "user@example.com" },
    scope: "openid email profile",
  },
  error: null,
};

describe("buildSignInRedirect", () => {
  it("preserves authorization_id through the sign-in round-trip", () => {
    const url = buildSignInRedirect(AUTH_ID);
    expect(url).toBe(
      `/auth/sign-in?next=${encodeURIComponent(
        `/oauth/consent?authorization_id=${AUTH_ID}`,
      )}`,
    );
  });
});

describe("ConsentPanel", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("shows an error and makes no Supabase calls when authorization_id is missing", async () => {
    render(<ConsentPanel authorizationId={null} />);

    await screen.findByRole("alert");
    expect(mockGetSession).not.toHaveBeenCalled();
  });

  it("redirects to sign-in, preserving authorization_id, when there is no session", async () => {
    mockGetSession.mockResolvedValue({ data: { session: null }, error: null });

    render(<ConsentPanel authorizationId={AUTH_ID} />);

    await waitFor(() =>
      expect(mockNavigate).toHaveBeenCalledWith(buildSignInRedirect(AUTH_ID)),
    );
    expect(mockGetAuthorizationDetails).not.toHaveBeenCalled();
  });

  it("renders client name and requested scopes when consent is needed", async () => {
    mockGetSession.mockResolvedValue(AUTHENTICATED_SESSION);
    mockGetAuthorizationDetails.mockResolvedValue(CONSENT_NEEDED_DETAILS);

    render(<ConsentPanel authorizationId={AUTH_ID} />);

    await screen.findByText(/Hive Chat/);
    expect(screen.getByText("openid")).toBeTruthy();
    expect(screen.getByText("email")).toBeTruthy();
    expect(screen.getByText("profile")).toBeTruthy();
    expect(screen.getByRole("button", { name: /approve/i })).toBeTruthy();
    expect(screen.getByRole("button", { name: /deny/i })).toBeTruthy();
  });

  it("hands off immediately when the server reports the user already consented", async () => {
    mockGetSession.mockResolvedValue(AUTHENTICATED_SESSION);
    mockGetAuthorizationDetails.mockResolvedValue({
      data: { redirect_url: "https://owui.example.com/callback?code=xyz" },
      error: null,
    });

    render(<ConsentPanel authorizationId={AUTH_ID} />);

    await waitFor(() =>
      expect(mockNavigate).toHaveBeenCalledWith(
        "https://owui.example.com/callback?code=xyz",
      ),
    );
  });

  it("approves the authorization and follows the returned redirect_url", async () => {
    mockGetSession.mockResolvedValue(AUTHENTICATED_SESSION);
    mockGetAuthorizationDetails.mockResolvedValue(CONSENT_NEEDED_DETAILS);
    mockApproveAuthorization.mockResolvedValue({
      data: { redirect_url: "https://owui.example.com/callback?code=approved" },
      error: null,
    });

    render(<ConsentPanel authorizationId={AUTH_ID} />);
    await screen.findByRole("button", { name: /approve/i });
    fireEvent.click(screen.getByRole("button", { name: /approve/i }));

    expect(mockApproveAuthorization).toHaveBeenCalledWith(AUTH_ID, {
      skipBrowserRedirect: true,
    });
    await waitFor(() =>
      expect(mockNavigate).toHaveBeenCalledWith(
        "https://owui.example.com/callback?code=approved",
      ),
    );
  });

  it("denies the authorization and follows the returned redirect_url", async () => {
    mockGetSession.mockResolvedValue(AUTHENTICATED_SESSION);
    mockGetAuthorizationDetails.mockResolvedValue(CONSENT_NEEDED_DETAILS);
    mockDenyAuthorization.mockResolvedValue({
      data: { redirect_url: "https://owui.example.com/callback?error=access_denied" },
      error: null,
    });

    render(<ConsentPanel authorizationId={AUTH_ID} />);
    await screen.findByRole("button", { name: /deny/i });
    fireEvent.click(screen.getByRole("button", { name: /deny/i }));

    expect(mockDenyAuthorization).toHaveBeenCalledWith(AUTH_ID, {
      skipBrowserRedirect: true,
    });
    await waitFor(() =>
      expect(mockNavigate).toHaveBeenCalledWith(
        "https://owui.example.com/callback?error=access_denied",
      ),
    );
  });

  it("surfaces a getAuthorizationDetails error as an alert instead of redirecting", async () => {
    mockGetSession.mockResolvedValue(AUTHENTICATED_SESSION);
    mockGetAuthorizationDetails.mockResolvedValue({
      data: null,
      error: { message: "authorization request expired" },
    });

    render(<ConsentPanel authorizationId={AUTH_ID} />);

    const alert = await screen.findByRole("alert");
    expect(alert.textContent).toContain("authorization request expired");
    expect(mockNavigate).not.toHaveBeenCalled();
  });

  it("surfaces an approve error as an alert instead of redirecting", async () => {
    mockGetSession.mockResolvedValue(AUTHENTICATED_SESSION);
    mockGetAuthorizationDetails.mockResolvedValue(CONSENT_NEEDED_DETAILS);
    mockApproveAuthorization.mockResolvedValue({
      data: null,
      error: { message: "network error" },
    });

    render(<ConsentPanel authorizationId={AUTH_ID} />);
    await screen.findByRole("button", { name: /approve/i });
    fireEvent.click(screen.getByRole("button", { name: /approve/i }));

    const alert = await screen.findByRole("alert");
    expect(alert.textContent).toContain("network error");
    expect(mockNavigate).not.toHaveBeenCalled();
  });
});
