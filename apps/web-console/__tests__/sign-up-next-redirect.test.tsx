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
});
