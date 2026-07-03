/**
 * TDD: /auth/sign-in honors the `?next=` redirect param on successful
 * sign-in (issue #269 — needed for the OWUI OIDC consent round-trip: the
 * consent page bounces an unauthenticated user to
 * /auth/sign-in?next=/oauth/consent?authorization_id=..., and sign-in must
 * send them back there instead of always landing on /console).
 */
import { beforeEach, describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen } from "@testing-library/react";

const mockSignInWithPassword = vi.fn();

vi.mock("@/lib/supabase/browser", () => ({
  createClient: () => ({
    auth: { signInWithPassword: mockSignInWithPassword },
  }),
}));

// jsdom's window.location.assign is non-configurable/non-writable, so it
// cannot be spied on directly. The sign-in page calls the small navigate()
// wrapper instead, which we mock here. The vi.fn() must live inside the
// factory (not a hoisted-above const) or vitest's hoisting hits a TDZ error.
vi.mock("@/lib/navigate", () => ({
  navigate: vi.fn(),
}));

import SignInPage from "../app/auth/sign-in/page";
import { navigate } from "@/lib/navigate";

const mockNavigate = vi.mocked(navigate);

describe("app/auth/sign-in/page.tsx next-target redirect", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mockSignInWithPassword.mockResolvedValue({ error: null });
    window.history.pushState({}, "", "/auth/sign-in");
  });

  async function submitForm() {
    render(<SignInPage />);
    // Regex match: the Field component appends a required "*" to the label
    // text ("Email*"), so an exact string match would never hit.
    fireEvent.change(screen.getByLabelText(/^email/i), {
      target: { value: "user@example.com" },
    });
    fireEvent.change(screen.getByLabelText(/^password/i), {
      target: { value: "hunter2hunter2" },
    });
    fireEvent.click(screen.getByRole("button", { name: /continue/i }));
    // Flush the async handleSubmit microtasks.
    await vi.waitFor(() => expect(mockSignInWithPassword).toHaveBeenCalled());
  }

  it("redirects to /console when no next param is present", async () => {
    await submitForm();
    await vi.waitFor(() => expect(mockNavigate).toHaveBeenCalledWith("/console"));
  });

  it("redirects to the allow-listed /oauth/consent next target, preserving authorization_id", async () => {
    window.history.pushState(
      {},
      "",
      `/auth/sign-in?next=${encodeURIComponent(
        "/oauth/consent?authorization_id=auth-req-123",
      )}`,
    );
    await submitForm();
    await vi.waitFor(() =>
      expect(mockNavigate).toHaveBeenCalledWith(
        "/oauth/consent?authorization_id=auth-req-123",
      ),
    );
  });

  it("ignores an unlisted next target and falls back to /console", async () => {
    window.history.pushState({}, "", `/auth/sign-in?next=${encodeURIComponent("/evil")}`);
    await submitForm();
    await vi.waitFor(() => expect(mockNavigate).toHaveBeenCalledWith("/console"));
  });

  it("does not redirect when sign-in fails", async () => {
    mockSignInWithPassword.mockResolvedValue({
      error: { message: "Invalid credentials" },
    });
    render(<SignInPage />);
    // Regex match: the Field component appends a required "*" to the label
    // text ("Email*"), so an exact string match would never hit.
    fireEvent.change(screen.getByLabelText(/^email/i), {
      target: { value: "user@example.com" },
    });
    fireEvent.change(screen.getByLabelText(/^password/i), {
      target: { value: "wrong" },
    });
    fireEvent.click(screen.getByRole("button", { name: /continue/i }));
    await screen.findByText("Invalid credentials");
    expect(mockNavigate).not.toHaveBeenCalled();
  });
});
