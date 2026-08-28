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

const mockNotFound = vi.fn(() => {
  throw Object.assign(new Error("NEXT_HTTP_ERROR_FALLBACK;404"), {
    digest: "NEXT_HTTP_ERROR_FALLBACK;404",
  });
});

const mockGetViewer = vi.fn();
const mockGetAccountProfile = vi.fn();
const mockGetProviders = vi.fn();
const mockGetFeatureGates = vi.fn();
const mockGetMarketplaceEntries = vi.fn();

vi.mock("next/navigation", () => ({
  redirect: () => {
    throw Object.assign(new Error("NEXT_REDIRECT"), {
      digest: "NEXT_REDIRECT",
    });
  },
  notFound: mockNotFound,
}));

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

// Structural subset of the control-plane viewer payload the role predicates
// read. No casts.
function viewerPayload(role: string, permissions: string[]) {
  return {
    user: { email: "caller@example.test", email_verified: true },
    current_account: {
      id: "a1",
      slug: "qa-workspace",
      display_name: "QA Workspace",
      account_type: "business",
      role,
    },
    memberships: [],
    permissions,
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

  it("renders feature gates and marketplace for a workspace owner", async () => {
    mockGetViewer.mockResolvedValue(
      viewerPayload("owner", [
        "members.invite",
        "members.manage",
        "workspace.settings",
      ]),
    );
    const [fgPage, mktPage] = await Promise.all([
      PAGES["feature-gates"],
      PAGES.marketplace,
    ]);

    render(await fgPage());
    expect(screen.getByTestId("console-shell")).toBeTruthy();

    cleanup();
    render(await mktPage());
    expect(screen.getByTestId("console-shell")).toBeTruthy();
  });

  it("renders all three surfaces for a platform admin", async () => {
    mockGetViewer.mockResolvedValue(viewerPayload("owner", ["platform.admin"]));
    const [providersPage, fgPage, mktPage] = await Promise.all([
      PAGES.providers,
      PAGES["feature-gates"],
      PAGES.marketplace,
    ]);

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
});

