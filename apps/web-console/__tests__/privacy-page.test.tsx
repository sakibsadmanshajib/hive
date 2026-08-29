/**
 * Tests for the console's privacy and data-policy surface
 * (/console/privacy). Every claim rendered on that page must be backed by
 * verified, enforced behaviour; these tests pin the specific truthful
 * statements the page is allowed to make, and several of them are negative
 * assertions that fail if the page drifts back into a broader claim than the
 * system keeps.
 *
 * Grounding for the assertions below:
 *   - The metering sentence is scoped to the usage record and phrased as
 *     behaviour. public.usage_events really does carry internal_metadata
 *     jsonb and customer_tags jsonb
 *     (supabase/migrations/20260330_02_usage_accounting.sql), so a
 *     structural "no field a body could land in" claim would be false. What
 *     strips content is usage.RedactMetadata, guarded on the Go side by
 *     TestUsageEventInsertWritesNoContentColumn and
 *     TestRedactMetadataStripsMessageContentKeys in
 *     apps/control-plane/internal/usage.
 *   - Batch, files and RAG do store content, so the page names them and
 *     these tests fail if that disclosure is removed.
 *   - Catalogue summaries still name vendors today (issue #1284, PR #1300),
 *     so the provider-blindness claim is scoped to error responses and this
 *     page rather than to every customer-facing surface.
 *   - routing.SelectRoute returns FallbackRouteIDs and nothing pins a
 *     fallback to the primary's provider, so the alias sentence must not
 *     claim a permanent 1:1 alias-to-route mapping.
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
  it("describes the metering record as behaviour, not as a structural impossibility", async () => {
    stubFetch();

    const mod = await import("../app/console/privacy/page");
    const page = await mod.default();
    render(page);

    screen.getByText(/carries token counts, cost, status, model alias/i);
    screen.getByText(/stripped from that metadata before the record is written/i);
  });

  it("never restates the blanket claim that the gateway stores no content", async () => {
    stubFetch();

    const mod = await import("../app/console/privacy/page");
    const page = await mod.default();
    render(page);

    expect(screen.queryByText(/does not store the content of your requests/i)).toBeNull();
    expect(screen.queryByText(/no field in that record a body could land in/i)).toBeNull();
  });

  it("names the three surfaces that do store content, so a batch, files or RAG user is not misled", async () => {
    stubFetch();

    const mod = await import("../app/console/privacy/page");
    const page = await mod.default();
    render(page);

    screen.getByText(/Batch jobs\./i);
    screen.getByText(/holds your request bodies verbatim/i);
    screen.getByText(/File uploads\./i);
    screen.getByText(/stored until you delete it through that same API/i);
    screen.getByText(/RAG documents\./i);
    screen.getByText(/text chunks the retrieval index searches/i);
  });

  it("discloses that requests leave the deployment boundary to a third-party provider, without implying data never leaves the box", async () => {
    stubFetch();

    const mod = await import("../app/console/privacy/page");
    const page = await mod.default();
    render(page);

    screen.getByText(/leaves this deployment's infrastructure boundary/i);
  });

  it("does not name which specific vendor serves which model, matching the console-wide provider-blind convention", async () => {
    stubFetch();

    const mod = await import("../app/console/privacy/page");
    const page = await mod.default();
    render(page);

    expect(screen.queryByText(/openrouter/i)).toBeNull();
    expect(screen.queryByText(/\bgroq\b/i)).toBeNull();
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

  it("scopes provider blindness to error responses and this page, not to every customer-facing surface", async () => {
    stubFetch();

    const mod = await import("../app/console/privacy/page");
    const page = await mod.default();
    render(page);

    screen.getByText(/not named in error responses and is not shown on this page/i);
    expect(screen.queryByText(/out of every customer-facing surface/i)).toBeNull();
  });

  it("discloses the data-collection posture, including that it is not set on every model", async () => {
    stubFetch();

    const mod = await import("../app/console/privacy/page");
    const page = await mod.default();
    render(page);

    screen.getByText(/refuse upstream providers that collect user data/i);
    screen.getByText(/not set on every model/i);
  });

  it("does not claim a permanent one-to-one alias-to-route mapping", async () => {
    stubFetch();

    const mod = await import("../app/console/privacy/page");
    const page = await mod.default();
    render(page);

    screen.getByText(/can have more than one eligible route/i);
    expect(screen.queryByText(/resolves to exactly one upstream route/i)).toBeNull();
  });

  it("shows a fetch failure as a load error, never as an empty catalog", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async (url: string) => {
        if (url.endsWith("/api/v1/viewer")) return jsonResponse(200, VIEWER_PAYLOAD);
        if (url.endsWith("/api/v1/accounts/current/profile")) {
          return jsonResponse(200, PROFILE_PAYLOAD);
        }
        if (url.endsWith("/api/v1/catalog/models")) return jsonResponse(500, {});
        throw new Error("unexpected fetch: " + url);
      }),
    );

    const mod = await import("../app/console/privacy/page");
    const page = await mod.default();
    render(page);

    screen.getByText(/model catalog could not be loaded right now/i);
    expect(screen.queryByText(/exposes no models in its catalog/i)).toBeNull();
    screen.getByText(/leaves this deployment/i);
  });

  it("shows an empty catalog as an empty catalog", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async (url: string) => {
        if (url.endsWith("/api/v1/viewer")) return jsonResponse(200, VIEWER_PAYLOAD);
        if (url.endsWith("/api/v1/accounts/current/profile")) {
          return jsonResponse(200, PROFILE_PAYLOAD);
        }
        if (url.endsWith("/api/v1/catalog/models")) {
          return jsonResponse(200, { models: [] });
        }
        throw new Error("unexpected fetch: " + url);
      }),
    );

    const mod = await import("../app/console/privacy/page");
    const page = await mod.default();
    render(page);

    screen.getByText(/exposes no models in its catalog/i);
    expect(screen.queryByText(/could not be loaded right now/i)).toBeNull();
  });

  it("states what it does not cover, so it does not read as a complete privacy statement", async () => {
    stubFetch();

    const mod = await import("../app/console/privacy/page");
    const page = await mod.default();
    render(page);

    screen.getByText(/What this page does not cover/i);
    screen.getByText(/physically\s+located/i);
    screen.getByText(/Whether Hive personnel can read stored content/i);
    screen.getByText(/Incident and breach notification/i);
    screen.getByText(/published in\s+English only today/i);
  });
});
