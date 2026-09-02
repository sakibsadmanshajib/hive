/**
 * TDD: /auth/sign-in honors the `?next=` redirect param on successful
 * sign-in (issue #269 — needed for the OWUI OIDC consent round-trip: the
 * consent page bounces an unauthenticated user to
 * /auth/sign-in?next=/oauth/consent?authorization_id=..., and sign-in must
 * send them back there instead of always landing on /console).
 */
import { beforeEach, describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen } from "@testing-library/react";
import { renderToStaticMarkup } from "react-dom/server";

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

// The page reads `next` with useSearchParams so the value exists on the very
// first render, server included. There is no Next router context outside the
// app, so stand in a params object parsed from window.location -- which is
// already what every case below drives, via window.history.pushState. Next
// itself resolves this hook from the request URL on the server render, because
// the root layout awaits cookies() (next-intl) and so every route is dynamic.
// Only the navigation calls this test drives are replaced.
// unstable_rethrow() stays real: the console's reads call it first in
// every catch so a framework throw is never classified as a data
// failure, and a stubbed one would pass whether or not that holds.
vi.mock("next/navigation", async (importOriginal) => {
  const actual = await importOriginal<typeof import("next/navigation")>();
  return {
    ...actual,
  useSearchParams: () => new URLSearchParams(window.location.search),
  };
});

import SignInPage from "../app/auth/sign-in/page";
import { navigate } from "@/lib/navigate";

const mockNavigate = vi.mocked(navigate);

describe("app/auth/sign-in/page.tsx next-target redirect", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mockSignInWithPassword.mockResolvedValue({ error: null });
    window.history.pushState({}, "", "/auth/sign-in");
    // The cross-link cases below are the self-serve-enabled shape. The
    // flag fails closed (lib/auth/self-serve.ts), so it has to be set
    // explicitly here or every link assertion would be testing the
    // invitation-only footer instead (issue #1328).
    process.env.NEXT_PUBLIC_DISABLE_SELF_SERVE_SIGNUP = "false";
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

  // Issue #534: the invitee arrives from an invite link, signs in, and must land
  // back on acceptance with the token intact.
  it("redirects back to /invitations/accept with the token preserved", async () => {
    window.history.pushState(
      {},
      "",
      `/auth/sign-in?next=${encodeURIComponent(
        "/invitations/accept?token=invite-token-9",
      )}`,
    );
    await submitForm();
    await vi.waitFor(() =>
      expect(mockNavigate).toHaveBeenCalledWith(
        "/invitations/accept?token=invite-token-9",
      ),
    );
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

  it("plain 'Create one' link has no next param when none is present", async () => {
    render(<SignInPage />);
    const link = await screen.findByRole("link", { name: /create one/i });
    expect(link.getAttribute("href")).toBe("/auth/sign-up");
  });

  it("'Create one' link carries the current next param through to sign-up (issue: OAuth consent round-trip dropped on signup)", async () => {
    window.history.pushState(
      {},
      "",
      `/auth/sign-in?next=${encodeURIComponent(
        "/oauth/consent?authorization_id=auth-req-123",
      )}`,
    );
    render(<SignInPage />);
    const link = await screen.findByRole("link", { name: /create one/i });
    expect(link.getAttribute("href")).toBe(
      `/auth/sign-up?next=${encodeURIComponent(
        "/oauth/consent?authorization_id=auth-req-123",
      )}`,
    );
  });

  // OWUI nightly run 31042840516: the OIDC setup click landed before React
  // hydrated, so the browser submitted the form natively. The form has no
  // `action` and its inputs have no `name`, so that GET rewrote
  // /auth/sign-in?next=%2Foauth%2Fconsent%3Fauthorization_id%3D... to
  // /auth/sign-in? -- `next` gone. The retry then signed in for real and
  // landed on /console, abandoning the consent round-trip, which cost the run
  // 86s and read as flake. The pre-hydration markup must therefore not offer
  // a submittable button at all (a disabled default button also blocks
  // implicit Enter submission).
  it("does not expose a submittable button before hydration", () => {
    // Parse the markup instead of substring-matching it: the Button's class
    // list carries Tailwind `disabled:*` variants, so a text search for
    // "disabled" passes even on an enabled button.
    const container = document.createElement("div");
    container.innerHTML = renderToStaticMarkup(<SignInPage />);
    const submit = container.querySelector<HTMLButtonElement>(
      'button[type="submit"]',
    );
    expect(submit).not.toBeNull();
    expect(submit?.disabled).toBe(true);
  });

  it("enables the submit button once mounted", async () => {
    render(<SignInPage />);
    const button = await screen.findByRole("button", { name: /continue/i });
    expect((button as HTMLButtonElement).disabled).toBe(false);
  });

  // Live login review: a user arriving from chat's "Continue with Hive" button
  // was shown a developer-console pitch (API keys, credits, usage analytics)
  // for what is, to them, simply signing in to Hive. The copy must follow the
  // journey, and must keep the console framing for people who came to the
  // console directly.
  describe("headline copy follows the journey", () => {
    it("uses neutral product copy when next is an OAuth consent target", async () => {
      window.history.pushState(
        {},
        "",
        `/auth/sign-in?next=${encodeURIComponent(
          "/oauth/consent?authorization_id=auth-req-123",
        )}`,
      );
      render(<SignInPage />);
      expect(
        await screen.findByRole("heading", { name: "Sign in to Hive" }),
      ).toBeTruthy();
      expect(screen.queryByText(/console/i)).toBeNull();
      expect(screen.queryByText(/API keys, credits/i)).toBeNull();
    });

    it("keeps the console copy for a direct console visit", async () => {
      render(<SignInPage />);
      expect(
        await screen.findByRole("heading", { name: "Sign in to your console" }),
      ).toBeTruthy();
      expect(
        screen.getByText(/Manage API keys, credits, and usage analytics/i),
      ).toBeTruthy();
    });

    // The neutral copy is gated on the same allow-list that gates the redirect,
    // so an attacker-supplied `next` cannot reach it either.
    //
    // This case is not a plain-revert guard and does not pretend to be one:
    // delete `fromConsent` and the heading is the console copy again, so it
    // reads green either way. What it pins is the *shape* of the check. A naive
    // `nextParam?.startsWith("/oauth/consent")` -- the obvious way to rewrite
    // this later -- matches "/oauth/consent-evil" and hands an attacker-chosen
    // target the neutral copy; that turns the heading and subtitle assertions
    // red. The static-markup assertion pins the same property on the first
    // render, where the copy is actually decided. The redirect half of the
    // allow-list is pinned separately by "ignores an unlisted next target and
    // falls back to /console" above, on the same input.
    it("keeps the console copy for an unlisted next target", async () => {
      window.history.pushState(
        {},
        "",
        `/auth/sign-in?next=${encodeURIComponent("/oauth/consent-evil")}`,
      );
      expect(renderToStaticMarkup(<SignInPage />)).not.toContain(
        "Sign in to Hive",
      );
      render(<SignInPage />);
      expect(
        await screen.findByRole("heading", { name: "Sign in to your console" }),
      ).toBeTruthy();
      expect(
        screen.getByText(/Manage API keys, credits, and usage analytics/i),
      ).toBeTruthy();
      expect(
        screen.queryByRole("heading", { name: "Sign in to Hive" }),
      ).toBeNull();
    });

    // Finding 3 of the review of this PR: the branch has to be decided on the
    // first render, not in a post-mount effect, or the chat user this change
    // exists for gets a beat of the console pitch before it swaps. Server
    // markup IS that first render, so asserting on it is the only thing that
    // catches a regression back to the effect-based read -- every client-side
    // assertion above passes either way, because findBy* waits and by then the
    // effect has fired. Revert `useSearchParams()` to the old
    // `useState(null)` + `useEffect` pair and this is the assertion that goes
    // red, on "Sign in to your console" appearing in the pre-hydration markup.
    it("decides the copy on the first render, before any effect runs", () => {
      window.history.pushState(
        {},
        "",
        `/auth/sign-in?next=${encodeURIComponent(
          "/oauth/consent?authorization_id=auth-req-123",
        )}`,
      );
      const markup = renderToStaticMarkup(<SignInPage />);
      expect(markup).toContain("Sign in to Hive");
      expect(markup).not.toContain("Sign in to your console");
      expect(markup).not.toContain("API keys, credits");
    });

    it("keeps the console copy in the first render for a direct visit", () => {
      const markup = renderToStaticMarkup(<SignInPage />);
      expect(markup).toContain("Sign in to your console");
      expect(markup).not.toContain("Sign in to Hive");
    });

    // The "Create one" cross-link is driven by the same value, so it carries
    // the target on the first render too instead of appearing bare and then
    // gaining a query string.
    it("carries next into the 'Create one' link on the first render", () => {
      window.history.pushState(
        {},
        "",
        `/auth/sign-in?next=${encodeURIComponent(
          "/oauth/consent?authorization_id=auth-req-123",
        )}`,
      );
      expect(renderToStaticMarkup(<SignInPage />)).toContain(
        `/auth/sign-up?next=${encodeURIComponent(
          "/oauth/consent?authorization_id=auth-req-123",
        )}`,
      );
    });
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

describe("app/auth/sign-in/page.tsx sign-up cross-link gating", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    window.history.pushState({}, "", "/auth/sign-in");
  });

  it("offers the sign-up link when this deployment accepts self-serve signup", async () => {
    process.env.NEXT_PUBLIC_DISABLE_SELF_SERVE_SIGNUP = "false";
    render(<SignInPage />);
    const link = await screen.findByRole("link", { name: /create one/i });
    expect(link.getAttribute("href")).toBe("/auth/sign-up");
  });

  it("does not link to a sign-up page this deployment refuses (issue #1328)", () => {
    process.env.NEXT_PUBLIC_DISABLE_SELF_SERVE_SIGNUP = "true";
    render(<SignInPage />);
    expect(screen.queryByRole("link", { name: /create one/i })).toBeNull();
    expect(
      screen.getByText(/accounts on this deployment are created by invitation/i),
    ).toBeTruthy();
  });
});
