/**
 * TDD: /auth/sign-up copy + next-param propagation (live UI/UX pass,
 * 2026-07-26).
 *
 * Contract exercised here:
 * - Subtitle no longer promises an automatic free credit grant (there is no
 *   signup credit-grant code path in control-plane; credits are
 *   owner-discretionary only).
 * - The "Sign in" cross-link preserves the current ?next= param so a user
 *   bounced here mid-OAuth-consent (chat -> console sign-in -> sign-up)
 *   doesn't lose their way back if they meant to sign in instead.
 * - The Supabase signUp emailRedirectTo carries the same ?next= param so the
 *   post-email-confirmation /auth/callback redirect can honor it too (the
 *   full signup -> confirm-email -> callback chain, not just the link).
 */
import { beforeEach, describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen } from "@testing-library/react";

const mockSignUp = vi.fn();

vi.mock("@/lib/supabase/browser", () => ({
  createClient: () => ({
    auth: { signUp: mockSignUp },
  }),
}));

import SignUpPage from "../app/auth/sign-up/page";

describe("app/auth/sign-up/page.tsx", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mockSignUp.mockResolvedValue({ error: null });
    window.history.pushState({}, "", "/auth/sign-up");
    process.env.NEXT_PUBLIC_APP_URL = "http://localhost:3000";
    // Every case in this describe exercises the form, which only renders
    // where the deployment accepts self-serve signup. The flag fails
    // closed, so it is set explicitly (issue #1328).
    process.env.NEXT_PUBLIC_DISABLE_SELF_SERVE_SIGNUP = "false";
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue({ ok: true, json: async () => ({}) }),
    );
  });

  it("does not promise a free/automatic credit grant in the subtitle", async () => {
    render(<SignUpPage />);
    expect(screen.queryByText(/free to start/i)).toBeNull();
    await screen.findByText(/pay only for what you use, in bdt/i);
  });

  it("plain 'Sign in' link has no next param when none is present", async () => {
    render(<SignUpPage />);
    const link = await screen.findByRole("link", { name: /^sign in$/i });
    expect(link.getAttribute("href")).toBe("/auth/sign-in");
  });

  it("'Sign in' link carries the current next param through to sign-in", async () => {
    window.history.pushState(
      {},
      "",
      `/auth/sign-up?next=${encodeURIComponent(
        "/oauth/consent?authorization_id=auth-req-123",
      )}`,
    );
    render(<SignUpPage />);
    const link = await screen.findByRole("link", { name: /^sign in$/i });
    expect(link.getAttribute("href")).toBe(
      `/auth/sign-in?next=${encodeURIComponent(
        "/oauth/consent?authorization_id=auth-req-123",
      )}`,
    );
  });

  async function submitForm() {
    render(<SignUpPage />);
    fireEvent.change(screen.getByLabelText(/^email/i), {
      target: { value: "user@example.com" },
    });
    fireEvent.change(screen.getByLabelText(/^password/i), {
      target: { value: "hunter2hunter2" },
    });
    fireEvent.click(screen.getByRole("button", { name: /create account/i }));
    await vi.waitFor(() => expect(mockSignUp).toHaveBeenCalled());
  }

  it("emailRedirectTo has no next param when none is present", async () => {
    await submitForm();
    const call = mockSignUp.mock.calls[0][0];
    expect(call.options.emailRedirectTo).toBe(
      "http://localhost:3000/auth/callback",
    );
  });

  it("emailRedirectTo carries the current next param through to /auth/callback", async () => {
    window.history.pushState(
      {},
      "",
      `/auth/sign-up?next=${encodeURIComponent(
        "/oauth/consent?authorization_id=auth-req-123",
      )}`,
    );
    await submitForm();
    const call = mockSignUp.mock.calls[0][0];
    expect(call.options.emailRedirectTo).toBe(
      `http://localhost:3000/auth/callback?next=${encodeURIComponent(
        "/oauth/consent?authorization_id=auth-req-123",
      )}`,
    );
  });

  // Issue #534: an invitee with no account signs UP. The acceptance token has
  // to survive signup, the verification email, and the callback, or the invite
  // is unusable for exactly the people invites are for.
  it("emailRedirectTo carries an invitation token through to /auth/callback", async () => {
    window.history.pushState(
      {},
      "",
      `/auth/sign-up?next=${encodeURIComponent(
        "/invitations/accept?token=invite-token-9",
      )}`,
    );
    await submitForm();
    const call = mockSignUp.mock.calls[0][0];
    expect(call.options.emailRedirectTo).toBe(
      `http://localhost:3000/auth/callback?next=${encodeURIComponent(
        "/invitations/accept?token=invite-token-9",
      )}`,
    );
  });
});

/**
 * Issue #1328: this deployment refuses POST /auth/v1/signup at the gateway
 * and at the GoTrue flag, so the console must say accounts are created by
 * invitation instead of shipping a form that cannot complete, and must report
 * a refusal that does reach the endpoint as a refusal rather than an outage.
 */
describe("app/auth/sign-up/page.tsx self-serve gating", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mockSignUp.mockResolvedValue({ error: null });
    window.history.pushState({}, "", "/auth/sign-up");
    process.env.NEXT_PUBLIC_APP_URL = "http://localhost:3000";
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue({ ok: true, json: async () => ({}) }),
    );
  });

  it("renders no sign-up form when the deployment refuses self-serve signup", () => {
    process.env.NEXT_PUBLIC_DISABLE_SELF_SERVE_SIGNUP = "true";
    render(<SignUpPage />);
    expect(screen.queryByLabelText(/^email/i)).toBeNull();
    expect(screen.queryByLabelText(/^password/i)).toBeNull();
    expect(screen.queryByRole("button", { name: /create account/i })).toBeNull();
  });

  it("says accounts are created by invitation, and points at sign-in", async () => {
    process.env.NEXT_PUBLIC_DISABLE_SELF_SERVE_SIGNUP = "true";
    render(<SignUpPage />);
    expect(
      screen.getByRole("heading", { name: /accounts are created by invitation/i }),
    ).toBeTruthy();
    expect(
      screen.getByText(/sign-up is not available on this deployment/i),
    ).toBeTruthy();
    const link = await screen.findByRole("link", { name: /go to sign in/i });
    expect(link.getAttribute("href")).toBe("/auth/sign-in");
  });

  it("carries an inbound next param into the sign-in link on the gated page", async () => {
    process.env.NEXT_PUBLIC_DISABLE_SELF_SERVE_SIGNUP = "true";
    window.history.pushState(
      {},
      "",
      `/auth/sign-up?next=${encodeURIComponent(
        "/oauth/consent?authorization_id=auth-req-123",
      )}`,
    );
    render(<SignUpPage />);
    const link = await screen.findByRole("link", { name: /go to sign in/i });
    expect(link.getAttribute("href")).toBe(
      `/auth/sign-in?next=${encodeURIComponent(
        "/oauth/consent?authorization_id=auth-req-123",
      )}`,
    );
  });

  it("reports a gateway refusal as a refusal, not as an outage on our end", async () => {
    process.env.NEXT_PUBLIC_DISABLE_SELF_SERVE_SIGNUP = "false";
    mockSignUp.mockResolvedValue({
      error: { name: "AuthUnknownError", message: "Unexpected end of JSON input" },
    });
    render(<SignUpPage />);
    fireEvent.change(screen.getByLabelText(/^email/i), {
      target: { value: "user@example.com" },
    });
    fireEvent.change(screen.getByLabelText(/^password/i), {
      target: { value: "hunter2hunter2" },
    });
    fireEvent.click(screen.getByRole("button", { name: /create account/i }));
    const alert = await screen.findByRole("alert");
    expect(alert.textContent).toMatch(/sign-up is not available on this deployment/i);
    expect(alert.textContent).not.toMatch(/something went wrong on our end/i);
  });
});
