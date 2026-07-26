import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, screen, fireEvent, waitFor, cleanup } from "@testing-library/react";

// Regression guard: a successful sign-in used to navigate to a bare "/tasks",
// which is outside Caddy's /agent-workspace/* route and was served by Open
// WebUI's catch-all -- the user landed on OWUI's login page instead of the
// task console. window.location.assign takes a raw URL and gets no basePath
// treatment from Next.js, so the target must carry the prefix itself.
let signInResult: { error: { message: string } | null } = { error: null };
const signInWithPassword = vi.fn(() => Promise.resolve(signInResult));

vi.mock("@/lib/supabase/browser", () => ({
  createClient: () => ({ auth: { signInWithPassword } }),
}));

import SignInPage from "./page";

describe("agent-console sign-in redirect", () => {
  const assign = vi.fn();

  beforeEach(() => {
    signInResult = { error: null };
    signInWithPassword.mockClear();
    assign.mockClear();
    Object.defineProperty(window, "location", {
      configurable: true,
      value: { assign },
    });
  });

  afterEach(() => {
    cleanup();
  });

  function submit() {
    fireEvent.change(screen.getByLabelText("Email"), {
      target: { value: "owner@example.com" },
    });
    fireEvent.change(screen.getByLabelText("Password"), {
      target: { value: "pw" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Sign in" }));
  }

  it("navigates to the basePath-prefixed task console on success", async () => {
    render(<SignInPage />);
    submit();

    await waitFor(() => expect(assign).toHaveBeenCalledTimes(1));
    expect(assign).toHaveBeenCalledWith("/agent-workspace/tasks");
  });

  it("does not navigate when the credentials are rejected", async () => {
    signInResult = { error: { message: "Invalid login credentials" } };
    render(<SignInPage />);
    submit();

    await waitFor(() => expect(screen.getByRole("alert")).toBeTruthy());
    expect(assign).not.toHaveBeenCalled();
  });
});
