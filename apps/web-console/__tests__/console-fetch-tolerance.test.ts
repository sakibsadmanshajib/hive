// @vitest-environment node
//
// Pure async functions, no DOM. Running this in node rather than the project
// default jsdom keeps one more browser environment out of an already
// environment-heavy suite.
import { beforeEach, describe, expect, it, vi } from "vitest";

// redirect() throws in Next.js. Reproducing that here is the whole point: a
// helper that swallowed it would silently render a signed-out page instead of
// sending the caller to sign-in.
const mockRedirect = vi.fn((target: string) => {
  throw new Error(`NEXT_REDIRECT:${target}`);
});

// Only redirect() is replaced. unstable_rethrow() stays real, because the
// tests below check that Next.js's own control-flow errors survive these
// helpers, and a stubbed rethrow would pass whether or not they do.
vi.mock("next/navigation", async (importOriginal) => {
  const actual = await importOriginal<typeof import("next/navigation")>();
  return { ...actual, redirect: mockRedirect };
});

vi.mock("next/headers", () => ({
  cookies: vi.fn(async () => ({
    get: vi.fn(() => undefined),
    getAll: vi.fn(() => []),
  })),
}));

const mockGetUser = vi.fn();
const mockGetSession = vi.fn();

vi.mock("../lib/supabase/server", () => ({
  createClient: vi.fn(() => ({
    auth: { getUser: mockGetUser, getSession: mockGetSession },
  })),
}));

const VIEWER_PAYLOAD = {
  user: { id: "u1", email: "qa@example.test", email_verified: true },
  current_account: {
    id: "a1",
    display_name: "QA Workspace",
    account_type: "personal",
    role: "owner",
  },
  memberships: [
    { account_id: "a1", display_name: "QA Workspace", role: "owner", status: "active" },
  ],
  permissions: [],
};

const PROFILE_PAYLOAD = {
  owner_name: "QA Owner",
  login_email: "qa@example.test",
  display_name: "QA Workspace",
  account_type: "personal",
  country_code: "BD",
  state_region: "Dhaka",
  profile_setup_complete: true,
};

function jsonResponse(status: number, body: unknown): Response {
  return new Response(JSON.stringify(body), { status });
}

/** Answers each control-plane path with the status this case is exercising. */
function routeFetch(statuses: { viewer?: number; profile?: number }) {
  return vi.fn(async (url: string) => {
    if (url.endsWith("/api/v1/viewer")) {
      const status = statuses.viewer ?? 200;
      return status === 200
        ? jsonResponse(200, VIEWER_PAYLOAD)
        : jsonResponse(status, { error: "viewer unavailable" });
    }
    if (url.endsWith("/api/v1/accounts/current/profile")) {
      const status = statuses.profile ?? 200;
      return status === 200
        ? jsonResponse(200, PROFILE_PAYLOAD)
        : jsonResponse(status, { error: "profile unavailable" });
    }
    return jsonResponse(404, { error: "unrouted" });
  });
}

beforeEach(() => {
  vi.resetModules();
  mockRedirect.mockClear();
  process.env.CONTROL_PLANE_BASE_URL = "http://control-plane.test";
  mockGetUser.mockResolvedValue({ data: { user: { id: "u1" } }, error: null });
  mockGetSession.mockResolvedValue({
    data: { session: { access_token: "token" } },
  });
});

describe("requireViewer", () => {
  it("returns the viewer when the control-plane answers", async () => {
    vi.stubGlobal("fetch", routeFetch({}));
    const { requireViewer } = await import("../lib/console/data");

    const viewer = await requireViewer();

    expect(viewer.current_account.display_name).toBe("QA Workspace");
    expect(mockRedirect).not.toHaveBeenCalled();
  });

  // The defect this closes: 19 console Server Components called getViewer()
  // bare, so one 503 from the viewer endpoint threw out of the page (or, from
  // the root layout, out of every console route) into the generic error
  // boundary. A viewer has no honest degraded form — without it there is no
  // workspace, no membership list and no shell — so the tolerance is a
  // redirect to sign-in, the same destination an expired session already
  // takes, applied once here instead of 19 times or not at all.
  it("redirects to sign-in instead of throwing when the viewer fetch fails", async () => {
    vi.stubGlobal("fetch", routeFetch({ viewer: 503 }));
    const { requireViewer } = await import("../lib/console/data");

    await expect(requireViewer()).rejects.toThrow("NEXT_REDIRECT:/auth/sign-in");
    expect(mockRedirect).toHaveBeenCalledWith("/auth/sign-in");
  });

  it("redirects to sign-in when the session itself cannot be resolved", async () => {
    mockGetUser.mockResolvedValue({ data: { user: null }, error: null });
    vi.stubGlobal("fetch", routeFetch({}));
    const { requireViewer } = await import("../lib/console/data");

    await expect(requireViewer()).rejects.toThrow("NEXT_REDIRECT:/auth/sign-in");
  });
});

describe("requireAccountProfile", () => {
  it("returns the profile when the control-plane answers", async () => {
    vi.stubGlobal("fetch", routeFetch({}));
    const { requireAccountProfile } = await import("../lib/console/data");

    const profile = await requireAccountProfile();

    expect(profile?.owner_name).toBe("QA Owner");
  });

  // A fresh account genuinely has no profile row. That 404 is a real state,
  // not an outage, and the setup form must still render blank for it.
  it("keeps the 404 needs-setup profile distinguishable from an outage", async () => {
    vi.stubGlobal("fetch", routeFetch({ profile: 404 }));
    const { requireAccountProfile } = await import("../lib/console/data");

    const profile = await requireAccountProfile();

    expect(profile).not.toBeNull();
    expect(profile?.profile_setup_complete).toBe(false);
    expect(profile?.owner_name).toBe("");
  });

  // The false-state trap: an outage must not resolve to a profile object,
  // because EMPTY_ACCOUNT_PROFILE carries profile_setup_complete: false and
  // the overview page reads exactly that field to decide whether to nag the
  // user to finish a setup they already completed.
  it("returns null on a real failure rather than a fabricated empty profile", async () => {
    vi.stubGlobal("fetch", routeFetch({ profile: 500 }));
    const { requireAccountProfile } = await import("../lib/console/data");

    await expect(requireAccountProfile()).resolves.toBeNull();
  });
});

describe("tolerate", () => {
  it("passes a resolved value through", async () => {
    const { tolerate } = await import("../lib/console/data");

    await expect(tolerate(Promise.resolve(["a"]))).resolves.toEqual(["a"]);
  });

  it("resolves a rejection to null so the caller can render an unknown state", async () => {
    const { tolerate } = await import("../lib/console/data");

    await expect(tolerate(Promise.reject(new Error("boom")))).resolves.toBeNull();
  });

  it("never converts a rejection into a zero or an empty collection", async () => {
    const { tolerate } = await import("../lib/console/data");

    const result = await tolerate<number[]>(Promise.reject(new Error("boom")));

    expect(result).toBeNull();
    expect(result).not.toEqual([]);
    expect(result).not.toBe(0);
  });
});

describe("Next.js control flow", () => {
  // redirect(), notFound() and "this route read cookies so it cannot be
  // prerendered" are all signalled by throwing. A catch-all that answers them
  // with null (or with a redirect to sign-in) silently converts a framework
  // instruction into a fabricated result: the build log showed exactly that,
  // a DynamicServerError from the prerender pass being logged as "could not
  // load viewer".
  it("passes a redirect straight through tolerate instead of nulling it", async () => {
    const { redirect: realRedirect } =
      await vi.importActual<typeof import("next/navigation")>("next/navigation");
    const { tolerate } = await import("../lib/console/data");

    const controlFlow = (async () => realRedirect("/somewhere"))();

    await expect(tolerate(controlFlow)).rejects.toThrow();
  });

  it("passes a notFound straight through tolerate instead of nulling it", async () => {
    const { notFound } =
      await vi.importActual<typeof import("next/navigation")>("next/navigation");
    const { tolerate } = await import("../lib/console/data");

    const controlFlow = (async () => notFound())();

    await expect(tolerate(controlFlow)).rejects.toThrow();
  });
});
