/**
 * Security review of PR #788. The control-plane withholds the raw `config` of
 * a global catalogue row from a caller who may not curate it, because an MCP
 * server entry can carry a credential in its `env`. The console has to read
 * that response: decodeMarketplaceEntry treated an absent `config` as a
 * malformed row and rejected the whole list, which would have turned the
 * redaction into the same "could not load" wall issue #758 removed.
 */
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const mockGetUser = vi.fn();
const mockGetSession = vi.fn();

vi.mock("next/headers", () => ({
  cookies: vi.fn(async () => ({
    get: vi.fn(() => undefined),
    getAll: vi.fn(() => []),
  })),
}));

vi.mock("../lib/supabase/server", () => ({
  createClient: vi.fn(() => ({
    auth: { getUser: mockGetUser, getSession: mockGetSession },
  })),
}));

const BASE_URL = "http://control-plane.internal:8081";

const ENTRY = {
  id: "3bf18e5c-2ddd-4114-9205-61fb1184acc0",
  kind: "mcp_server",
  name: "github",
  description: "GitHub MCP server",
  enabled: false,
  created_at: "2026-08-08T00:00:00Z",
  updated_at: "2026-08-08T00:00:00Z",
};

let nextResponse: Response = new Response("", { status: 200 });

describe("marketplace catalogue read", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    process.env.CONTROL_PLANE_BASE_URL = BASE_URL;
    mockGetUser.mockResolvedValue({ data: { user: { id: "u1" } }, error: null });
    mockGetSession.mockResolvedValue({
      data: { session: { access_token: "ACCESS_TOKEN_PLACEHOLDER" } },
    });
    vi.stubGlobal("fetch", () => Promise.resolve(nextResponse));
  });

  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it("renders an entry whose config the control-plane withheld", async () => {
    nextResponse = new Response(
      JSON.stringify({ entries: [ENTRY], can_curate: false }),
      { status: 200 },
    );
    const { getMarketplaceEntries } = await import("../lib/control-plane/client");

    const catalogue = await getMarketplaceEntries();

    expect(catalogue.entries).toHaveLength(1);
    expect(catalogue.entries[0].name).toBe("github");
    expect(catalogue.entries[0].config).toEqual({});
    expect(catalogue.canCurate).toBe(false);
  });

  it("keeps the config a curator does receive", async () => {
    nextResponse = new Response(
      JSON.stringify({
        entries: [{ ...ENTRY, config: { command: "npx" } }],
        can_curate: true,
      }),
      { status: 200 },
    );
    const { getMarketplaceEntries } = await import("../lib/control-plane/client");

    const catalogue = await getMarketplaceEntries();

    expect(catalogue.entries[0].config).toEqual({ command: "npx" });
    expect(catalogue.canCurate).toBe(true);
  });

  it("still rejects a row whose config is present but not an object", async () => {
    nextResponse = new Response(
      JSON.stringify({
        entries: [{ ...ENTRY, config: "not-an-object" }],
        can_curate: true,
      }),
      { status: 200 },
    );
    const { getMarketplaceEntries } = await import("../lib/control-plane/client");

    await expect(getMarketplaceEntries()).rejects.toThrow(/parse/);
  });
});
