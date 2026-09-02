/**
 * Issues #947/#948/#949 family: hiding a nav entry is not access control.
 * The Providers, Feature gates and Marketplace console pages are
 * platform-operator surfaces. Providers is platform-admin only
 * (control-plane RequirePlatformAdmin); Feature gates and Marketplace admit
 * the administrator of the selected workspace or a platform admin
 * (control-plane WorkspaceAdminGate). A customer who types the URL must get a
 * 404, not a 200 page shell that confirms the surface exists.
 *
 * Enforcement layer: the Next.js server component itself calls notFound()
 * before any data fetch. Every data endpoint these pages use is already
 * enforced at the control-plane and forwarded as 403 by the Next.js proxy
 * routes (app/api/console/{providers,feature-gates,marketplace}).
 */
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { cleanup, render, screen } from "@testing-library/react";

import ProvidersPage from "../app/console/providers/page";
import FeatureGatesPage from "../app/console/feature-gates/page";
import MarketplacePage from "../app/console/marketplace/page";
import { ControlPlaneError, type Viewer } from "@/lib/control-plane/client";

// Hoisted: ProvidersPage/FeatureGatesPage/MarketplacePage are imported
// statically above, so evaluating those imports (and the next/navigation +
// control-plane/client modules they pull in) runs the vi.mock factories
// below before a plain top-level `const mock... = vi.fn()` would have
// executed. vi.hoisted guarantees these exist first regardless.
const {
  mockNotFound,
  mockGetViewer,
  mockGetAccountProfile,
  mockGetProviders,
  mockGetFeatureGates,
  mockGetMarketplaceEntries,
} = vi.hoisted(() => ({
  mockNotFound: vi.fn(() => {
    throw Object.assign(new Error("NEXT_HTTP_ERROR_FALLBACK;404"), {
      digest: "NEXT_HTTP_ERROR_FALLBACK;404",
    });
  }),
  mockGetViewer: vi.fn(),
  mockGetAccountProfile: vi.fn(),
  mockGetProviders: vi.fn(),
  mockGetFeatureGates: vi.fn(),
  mockGetMarketplaceEntries: vi.fn(),
}));

// Only redirect() and notFound() are replaced. unstable_rethrow() stays real:
// these pages call it first in their catch so a framework throw is never
// classified as a permission failure, and a stubbed-out version would make
// the assertions below pass whether or not that holds.
vi.mock("next/navigation", async (importOriginal) => {
  const actual = await importOriginal<typeof import("next/navigation")>();
  return {
    ...actual,
    redirect: () => {
      throw Object.assign(new Error("NEXT_REDIRECT"), {
        digest: "NEXT_REDIRECT",
      });
    },
    notFound: mockNotFound,
  };
});

vi.mock("@/components/app-shell/console-shell", () => ({
  ConsoleShell: ({ children }: { children: React.ReactNode }) => (
    <div data-testid="console-shell">{children}</div>
  ),
}));

vi.mock("@/lib/control-plane/client", () => ({
  getViewer: mockGetViewer,
  getAccountProfile: mockGetAccountProfile,
  getProviders: mockGetProviders,
  getFeatureGates: mockGetFeatureGates,
  getMarketplaceEntries: mockGetMarketplaceEntries,
  ControlPlaneError: class ControlPlaneError extends Error {
    status: number;
    code: string | null;
    constructor(status: number, message: string, code: string | null = null) {
      super(message);
      this.name = "ControlPlaneError";
      this.status = status;
      this.code = code;
    }
  },
}));

// Full Viewer fixture, typed against the real interface so a drift between
// this fixture and control-plane/client.ts's Viewer shape is a compile
// error, not a silent gap. No casts.
function viewerPayload(
  role: string,
  permissions: string[],
  workspaceAdmin = false,
): Viewer {
  return {
    user: {
      id: "u1",
      email: "caller@example.test",
      email_verified: true,
    },
    current_account: {
      id: "a1",
      slug: "qa-workspace",
      display_name: "QA Workspace",
      account_type: "business",
      role,
    },
    memberships: [],
    permissions,
    workspace_admin: workspaceAdmin,
  };
}

describe("server-side role gating of operator surfaces", () => {
  const SURFACES = ["providers", "feature-gates", "marketplace"] as const;

  beforeEach(() => {
    vi.clearAllMocks();
    // Data stubs only serve allowed paths: notFound() fires before any fetch.
    mockGetAccountProfile.mockResolvedValue({ owner_name: "" });
    mockGetProviders.mockResolvedValue([]);
    mockGetFeatureGates.mockResolvedValue({ gates: [] });
    mockGetMarketplaceEntries.mockResolvedValue({ entries: [] });
  });

  afterEach(() => {
    cleanup();
  });

  const PAGES = {
    providers: ProvidersPage,
    "feature-gates": FeatureGatesPage,
    marketplace: MarketplacePage,
  } as const;

  it.each(SURFACES)(
    "404s %s for a plain member, gate firing before any data fetch",
    async (surface) => {
      mockGetViewer.mockResolvedValue(viewerPayload("member", ["analytics.view"]));
      const Page = PAGES[surface];

      await expect(Promise.resolve(Page())).rejects.toMatchObject({
        digest: "NEXT_HTTP_ERROR_FALLBACK;404",
      });
      expect(mockNotFound).toHaveBeenCalledTimes(1);
      expect(mockGetAccountProfile).not.toHaveBeenCalled();
      expect(mockGetProviders).not.toHaveBeenCalled();
      expect(mockGetFeatureGates).not.toHaveBeenCalled();
      expect(mockGetMarketplaceEntries).not.toHaveBeenCalled();
    },
  );

  it("still 404s providers for a workspace owner without the platform-admin permission", async () => {
    mockGetViewer.mockResolvedValue(
      viewerPayload("owner", [
        "members.invite",
        "members.manage",
        "workspace.settings",
      ]),
    );
    const Page = PAGES.providers;
    await expect(Promise.resolve(Page())).rejects.toMatchObject({
      digest: "NEXT_HTTP_ERROR_FALLBACK;404",
    });
  });

  it("renders feature gates and marketplace for a tenant workspace administrator", async () => {
    mockGetViewer.mockResolvedValue(
      viewerPayload(
        "owner",
        ["members.invite", "members.manage", "workspace.settings"],
        true,
      ),
    );
    const fgPage = PAGES["feature-gates"];
    const mktPage = PAGES.marketplace;

    render(await fgPage());
    expect(screen.getByTestId("console-shell")).toBeTruthy();

    cleanup();
    render(await mktPage());
    expect(screen.getByTestId("console-shell")).toBeTruthy();
  });

  it("renders all three surfaces for a platform admin", async () => {
    mockGetViewer.mockResolvedValue(viewerPayload("owner", ["platform.admin"]));
    const providersPage = PAGES.providers;
    const fgPage = PAGES["feature-gates"];
    const mktPage = PAGES.marketplace;

    render(await providersPage());
    expect(screen.getByTestId("console-shell")).toBeTruthy();

    cleanup();
    render(await fgPage());
    expect(screen.getByTestId("console-shell")).toBeTruthy();

    cleanup();
    render(await mktPage());
    expect(screen.getByTestId("console-shell")).toBeTruthy();
  });

  it("admits providers on the platform-admin overlay alone, regardless of membership role", async () => {
    mockGetViewer.mockResolvedValue(
      viewerPayload("member", ["platform.admin"]),
    );
    const Page = PAGES.providers;

    await Promise.resolve(Page());
    expect(mockNotFound).not.toHaveBeenCalled();
    expect(mockGetProviders).toHaveBeenCalledTimes(1);
  });

  // The "renders all three surfaces for a platform admin" case above uses an
  // owner viewer, so it can't tell whether feature gates/marketplace let a
  // platform admin in via isPlatformAdminViewer(viewer) or via the
  // role === "owner" branch it also satisfies. This isolates the former: a
  // plain member who only carries the platform-admin overlay.
  it("admits feature gates and marketplace on the platform-admin overlay alone, regardless of membership role", async () => {
    mockGetViewer.mockResolvedValue(
      viewerPayload("member", ["platform.admin"]),
    );

    await Promise.resolve(PAGES["feature-gates"]());
    expect(mockNotFound).not.toHaveBeenCalled();
    expect(mockGetFeatureGates).toHaveBeenCalledTimes(1);

    await Promise.resolve(PAGES.marketplace());
    expect(mockNotFound).not.toHaveBeenCalled();
    expect(mockGetMarketplaceEntries).toHaveBeenCalledTimes(1);
  });

  // Issue #1660. A personal tenant's sole owner is 'owner' in
  // account_memberships and 'MEMBER' in tenant_users, deliberately
  // (signup.insertPersonalMembership). Gating on the account role admitted
  // them to a page whose data fetch the control-plane then answered 403,
  // leaving them on an empty state that told them to ask an administrator who
  // does not exist. The gate now reads the same tenant-scoped signal the
  // backend does, so the page refuses before any fetch, exactly as it does for
  // any other caller without workspace-administration authority.
  it("404s feature gates and marketplace for a personal-tenant sole owner", async () => {
    mockGetViewer.mockResolvedValue(
      viewerPayload("owner", ["members.invite", "workspace.settings"], false),
    );

    for (const surface of ["feature-gates", "marketplace"] as const) {
      await expect(Promise.resolve(PAGES[surface]())).rejects.toMatchObject({
        digest: "NEXT_HTTP_ERROR_FALLBACK;404",
      });
    }
    expect(mockGetFeatureGates).not.toHaveBeenCalled();
    expect(mockGetMarketplaceEntries).not.toHaveBeenCalled();
    expect(screen.queryByText(/administrator/i)).toBeNull();
  });

  // The other half of the same rule: a caller the control-plane genuinely
  // refuses still gets told where the authority sits. This is the residual
  // 403 path (a workspace administrator whose tenant membership changed
  // between the viewer read and the data fetch), and its copy is correct
  // there because somebody else really does hold the permission.
  it("keeps the administrator-referral empty state for a caller the control-plane refuses", async () => {
    mockGetViewer.mockResolvedValue(
      viewerPayload("owner", ["workspace.settings"], true),
    );
    const denied = new ControlPlaneError(
      403,
      "workspace owner permission required",
    );
    mockGetFeatureGates.mockRejectedValue(denied);
    mockGetMarketplaceEntries.mockRejectedValue(denied);

    render(await PAGES["feature-gates"]());
    expect(
      screen.getByText(
        "Ask your workspace owner or administrator if you need a capability turned on.",
      ),
    ).toBeTruthy();

    cleanup();
    render(await PAGES.marketplace());
    expect(
      screen.getByText(
        "Ask your workspace owner or administrator if you need a connector enabled.",
      ),
    ).toBeTruthy();
  });
});

