/**
 * Tests for the console's privacy and data-policy surface
 * (/console/privacy). Every claim rendered on that page must be backed by
 * verified, enforced behavior; these tests pin the specific truthful
 * statements the page is allowed to make and guard against it drifting into
 * an unenforced or false compliance claim.
 *
 * Grounding for the assertions below:
 *   - UsageEventRow (lib/control-plane/client.ts) carries no request/response
 *     content field, only token counts, cost, status, and identifiers, so
 *     the "no content stored" claim is a real schema property, not prose.
 *   - PublicCatalogModel / CatalogModel (control-plane and web-console) never
 *     carry a provider field; the console has never named an upstream
 *     provider on a live per-model basis, matching the provider-blind
 *     convention (apps/edge-api/internal/errors/provider_blind_test.go).
 *   - routing.SelectionInput.AllowedProviders exists on the wire type but no
 *     call site in edge-api ever sets it (verified 2026-08-28), so a
 *     per-tenant provider allow/block control cannot be claimed as real.
 */
import { beforeEach, describe, expect, it, vi } from "vitest";
import { render, screen } from "@testing-library/react";

const mockGetSession = vi.fn();
const mockGetUser = vi.fn();

vi.mock("next/headers", () => ({
  cookies: vi.fn(async () => ({
    get: vi.fn(() => undefined),
    getAll: vi.fn(() => []),
  })),
}));

vi.mock("next/navigation", () => ({
  redirect: vi.fn((path: string) => {
    throw new Error(`NEXT_REDIRECT:${path}`);
  }),
}));

vi.mock("../lib/supabase/server", () => ({
  createClient: vi.fn(() => ({
    auth: { getUser: mockGetUser, getSession: mockGetSession },
  })),
}));

vi.mock("@/components/app-shell/console-shell", () => ({
  ConsoleShell: ({ children }: { children: React.ReactNode }) => (
    <div data-testid="console-shell">{children}</div>
  ),
}));

const VIEWER_PAYLOAD = {
  user: { id: "u1", email: "qa@example.test", email_verified: true },
  current_account: {
    id: "a1",
    display_name: "QA Workspace",
    account_type: "personal",
    role: "owner",
    slug: "qa-workspace",
  },
  memberships: [
    { account_id: "a1", display_name: "QA Workspace", role: "owner", status: "active" },
  ],
  permissions: ["workspace.settings"],
};

const PROFILE_PAYLOAD = {
  owner_name: "Ada Owner",
  login_email: "ada@example.test",
  display_name: "QA Workspace",
  account_type: "personal",
  country_code: "CA",
  state_region: "ON",
  profile_setup_complete: true,
};

const CATALOG_PAYLOAD = {
  models: [
    {
      id: "hive-default",
      display_name: "Hive Default",
      summary: "Groq openai/gpt-oss-20b.",
      capability_badges: ["streaming"],
      pricing: {
        input_price_credits: 10500,
        output_price_credits: 42000,
        cache_read_price_credits: null,
        cache_write_price_credits: null,
        pricing_mode: "fixed",
      },
      lifecycle: "stable",
    },
    {
      id: "deepseek-v4-flash",
      display_name: "DeepSeek V4 Flash",
      summary: "OpenRouter deepseek/deepseek-v4-flash-latest.",
      capability_badges: ["streaming"],
      pricing: {
        input_price_credits: 1000,
        output_price_credits: 2000,
        cache_read_price_credits: null,
        cache_write_price_credits: null,
        pricing_mode: "fixed",
      },
      lifecycle: "stable",
    },
  ],
};

function jsonResponse(status: number, body: unknown): Response {
  return new Response(JSON.stringify(body), { status });
}

beforeEach(() => {
  vi.clearAllMocks();
  process.env.CONTROL_PLANE_BASE_URL = "http://localhost:8081";
  mockGetUser.mockResolvedValue({
    data: { user: { id: "u1", email: "qa@example.test" } },
    error: null,
  });
  mockGetSession.mockResolvedValue({
    data: { session: { access_token: "test-token" } },
  });
});

function stubFetch() {
  vi.stubGlobal(
    "fetch",
    vi.fn(async (url: string) => {
      if (url.endsWith("/api/v1/viewer")) return jsonResponse(200, VIEWER_PAYLOAD);
      if (url.endsWith("/api/v1/accounts/current/profile")) {
        return jsonResponse(200, PROFILE_PAYLOAD);
      }
      if (url.endsWith("/api/v1/catalog/models")) {
        return jsonResponse(200, CATALOG_PAYLOAD);
      }
      throw new Error(`unexpected fetch: ${url}`);
    }),
  );
}

describe("app/console/privacy/page.tsx", () => {
  it("states that request/response content is not stored, only metering metadata", async () => {
    stubFetch();

    const mod = await import("../app/console/privacy/page");
    const page = await mod.default();
    render(page);

    screen.getByText(/does not store the content of your requests or responses/i);
    screen.getByText(/token counts, cost, status, model alias/i);
  });

  it("names the real upstream providers instead of implying data never leaves the box", async () => {
    stubFetch();

    const mod = await import("../app/console/privacy/page");
    const page = await mod.default();
    render(page);

    screen.getByText(/OpenRouter/);
    screen.getByText(/Groq/);
  });

  it("renders the live catalog aliases rather than a hardcoded list", async () => {
    stubFetch();

    const mod = await import("../app/console/privacy/page");
    const page = await mod.default();
    render(page);

    screen.getByText("Hive Default");
    screen.getByText("DeepSeek V4 Flash");
  });

  it("discloses that per-tenant provider allow/block is not wired to any control today, rather than showing a fake toggle", async () => {
    stubFetch();

    const mod = await import("../app/console/privacy/page");
    const page = await mod.default();
    render(page);

    screen.getByText(/no tenant-facing control (?:persists|sets)/i);
    expect(screen.queryByRole("switch")).toBeNull();
    expect(screen.queryByRole("checkbox")).toBeNull();
  });

  it("scopes the statement to the API gateway and does not claim coverage of chat conversation storage", async () => {
    stubFetch();

    const mod = await import("../app/console/privacy/page");
    const page = await mod.default();
    render(page);

    screen.getByText(/does not describe/i);
  });

  it("links both interactive controls on the page to their real destinations", async () => {
    stubFetch();

    const mod = await import("../app/console/privacy/page");
    const page = await mod.default();
    render(page);

    const usageLogLink = screen.getByRole("link", { name: /view your usage log/i });
    expect(usageLogLink.getAttribute("href")).toBe("/console/logs");

    const apiKeysLink = screen.getByRole("link", { name: /manage api key model access/i });
    expect(apiKeysLink.getAttribute("href")).toBe("/console/api-keys");
  });

  it("redirects an unverified email to profile settings, matching every other console page", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async (url: string) => {
        if (url.endsWith("/api/v1/viewer")) {
          return jsonResponse(200, {
            ...VIEWER_PAYLOAD,
            user: { ...VIEWER_PAYLOAD.user, email_verified: false },
          });
        }
        throw new Error(`unexpected fetch: ${url}`);
      }),
    );

    const mod = await import("../app/console/privacy/page");
    await expect(mod.default()).rejects.toThrow("NEXT_REDIRECT:/console/settings/profile");
  });
});
