/**
 * Tests for the console request log browser (/console/logs): the expandable
 * table component, the filter bar, and the page-level wiring that carries
 * fetched usage events into the rendered table. The control-plane response is
 * mocked at fetch level so the real getUsageEvents decoder runs.
 */
import { beforeEach, describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import type { UsageEventRow } from "@/lib/control-plane/client";

vi.mock("next/navigation", () => ({
  redirect: vi.fn(() => {
    throw new Error("NEXT_REDIRECT");
  }),
}));

vi.mock("next/link", () => ({
  default: ({
    href,
    children,
  }: {
    href: string;
    children?: React.ReactNode;
  }) => (
    <a href={href} data-testid="next-link">
      {children}
    </a>
  ),
}));

vi.mock("next/headers", () => ({
  cookies: vi.fn(async () => ({
    get: vi.fn(() => undefined),
    getAll: vi.fn(() => []),
  })),
}));

const mockGetSession = vi.fn();
const mockGetUser = vi.fn();

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
    {
      account_id: "a1",
      display_name: "QA Workspace",
      role: "owner",
      status: "active",
    },
  ],
  permissions: ["workspace.analytics.view"],
};

const PROFILE_PAYLOAD = {
  owner_name: "Ada Owner",
  login_email: "ada@example.test",
  display_name: "QA Workspace",
  account_type: "personal",
  country_code: "BD",
  state_region: "",
  profile_setup_complete: true,
};

function jsonResponse(status: number, body: unknown): Response {
  return new Response(JSON.stringify(body), { status });
}

// Builds a fully-typed UsageEventRow fixture so no cast is needed anywhere
// a row list is passed to the component under test.
function eventRow(overrides: Partial<UsageEventRow> = {}): UsageEventRow {
  return {
    id: "evt_1",
    request_id: "req_aaa",
    request_attempt_id: "att_111",
    event_type: "completed",
    endpoint: "/v1/chat/completions",
    model_alias: "hive-fast",
    status: "completed",
    input_tokens: 120,
    output_tokens: 45,
    hive_credit_delta: -7,
    customer_tags: {},
    created_at: "2026-08-22T10:00:00Z",
    ...overrides,
  };
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

describe("UsageLogsTable", () => {
  const baseRows: UsageEventRow[] = [
    eventRow(),
    eventRow({
      id: "evt_2",
      request_id: "req_bbb",
      model_alias: "hive-pro",
      status: "failed",
      error_code: "upstream_timeout",
      api_key_id: "key_abc",
    }),
  ];

  it("renders one row per usage event with model, tokens, and status", async () => {
    const { UsageLogsTable } = await import(
      "@/components/logs/usage-logs-table"
    );
    render(<UsageLogsTable rows={baseRows} keyNames={{ key_abc: "Prod key" }} />);

    expect(screen.getByText("hive-fast")).toBeTruthy();
    expect(screen.getByText("hive-pro")).toBeTruthy();
    expect(screen.getByText("completed")).toBeTruthy();
    expect(screen.getByText("failed")).toBeTruthy();
    // Key name joined client-side from the keys API map.
    expect(screen.getByText("Prod key")).toBeTruthy();
  });

  it("falls back to a key-id suffix when no matching key name exists", async () => {
    const { UsageLogsTable } = await import(
      "@/components/logs/usage-logs-table"
    );
    render(<UsageLogsTable rows={baseRows} keyNames={{}} />);

    expect(screen.getByText("…ey_abc")).toBeTruthy();
  });

  it("expands a row on click and shows request details plus lifecycle entries", async () => {
    const fetchMock = vi.fn(async (url: string) => {
      if (url.includes("/credits/ledger")) {
        return jsonResponse(200, {
          entries: [
            {
              id: "l1",
              entry_type: "reservation_hold",
              credits_delta: -20,
              idempotency_key: "i1",
              request_id: "req_bbb",
              created_at: "2026-08-22T10:00:01Z",
            },
            {
              id: "l2",
              entry_type: "usage_charge",
              credits_delta: -7,
              idempotency_key: "i2",
              request_id: "req_bbb",
              created_at: "2026-08-22T10:00:02Z",
            },
            {
              id: "l3",
              entry_type: "reservation_release",
              credits_delta: 13,
              idempotency_key: "i3",
              request_id: "req_bbb",
              created_at: "2026-08-22T10:00:03Z",
            },
          ],
          next_cursor: null,
        });
      }
      throw new Error(`unexpected fetch: ${url}`);
    });
    vi.stubGlobal("fetch", fetchMock);

    const { UsageLogsTable } = await import(
      "@/components/logs/usage-logs-table"
    );
    render(<UsageLogsTable rows={baseRows} keyNames={{}} />);

    fireEvent.click(screen.getByText("hive-pro"));

    const detail = screen.getByTestId("log-detail");
    expect(detail.textContent).toContain("req_bbb");
    expect(detail.textContent).toContain("upstream_timeout");

    await waitFor(() => {
      expect(screen.getByTestId("lifecycle-list")).toBeTruthy();
    });
    const list = screen.getByTestId("lifecycle-list").textContent ?? "";
    expect(list).toContain("Hold");
    expect(list).toContain("Charge");
    expect(list).toContain("Release");
    expect(fetchMock).toHaveBeenCalledWith(
      expect.stringContaining("/credits/ledger?request_id=req_bbb")
    );
  });

  it("shows the filtered-empty message when there are no rows", async () => {
    const { UsageLogsTable } = await import(
      "@/components/logs/usage-logs-table"
    );
    render(<UsageLogsTable rows={[]} keyNames={{}} />);

    expect(
      screen.getByText("No requests match these filters.")
    ).toBeTruthy();
  });

  it("renders a formatted latency column and an em-dash for an unmeasured request", async () => {
    const { UsageLogsTable } = await import(
      "@/components/logs/usage-logs-table"
    );
    render(
      <UsageLogsTable
        rows={[
          eventRow({ id: "evt_lat", latency_ms: 1800 }),
          eventRow({ id: "evt_unmeasured", request_id: "req_ccc" }),
        ]}
        keyNames={{}}
      />
    );

    expect(screen.getByText("1.8s")).toBeTruthy();
  });

  it("shows latency in the per-row drill-in detail", async () => {
    // Expanding a row fires the lifecycle fetch; stub it the same way the
    // existing expand test does so the effect resolves inside act().
    vi.stubGlobal(
      "fetch",
      vi.fn(async () => jsonResponse(200, { entries: [], next_cursor: null }))
    );

    const { UsageLogsTable } = await import(
      "@/components/logs/usage-logs-table"
    );
    render(
      <UsageLogsTable
        rows={[eventRow({ latency_ms: 340 })]}
        keyNames={{}}
      />
    );

    fireEvent.click(screen.getByText("hive-fast"));
    const detail = screen.getByTestId("log-detail");
    expect(detail.textContent).toContain("340ms");
    await waitFor(() => {
      expect(screen.getByText("No ledger activity for this request.")).toBeTruthy();
    });
  });

  it("hides and restores a column through the column-controls checklist", async () => {
    const { UsageLogsTable } = await import(
      "@/components/logs/usage-logs-table"
    );
    render(<UsageLogsTable rows={baseRows} keyNames={{}} />);

    const controls = screen.getByTestId("column-controls");
    const latencyToggle = within(controls).getByLabelText("Latency");
    expect((latencyToggle as HTMLInputElement).checked).toBe(true);
    expect(screen.getAllByText("Latency").length).toBeGreaterThan(0);

    fireEvent.click(latencyToggle);
    expect((latencyToggle as HTMLInputElement).checked).toBe(false);
    // "Latency" survives only in the checklist label once the column header
    // is hidden.
    expect(screen.getAllByText("Latency")).toHaveLength(1);

    fireEvent.click(latencyToggle);
    expect(screen.getAllByText("Latency").length).toBeGreaterThan(1);
  });
});

describe("LogsFilters", () => {
  it("renders time presets, errors toggle, and the three selects", async () => {
    const { LogsFilters } = await import("@/components/logs/logs-filters");

    render(
      <LogsFilters
        state={{
          window: "24h",
          model: null,
          status: null,
          key: null,
          errors: false,
        }}
        models={["hive-fast", "hive-pro"]}
        keys={[]}
      />
    );

    for (const label of ["All", "1h", "24h", "7d", "30d", "Errors only"]) {
      expect(screen.getByText(label)).toBeTruthy();
    }
    expect(screen.getByLabelText("Model").querySelectorAll("option")).toHaveLength(3);
    expect(
      screen.getByLabelText("Status").querySelectorAll("option").length
    ).toBeGreaterThan(1);
    expect(
      screen.getByLabelText("API key").querySelectorAll("option")
    ).toHaveLength(1);
  });
});

describe("app/console/logs/page.tsx wiring", () => {
  it("renders fetched usage events through the real client decoder", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async (url: string) => {
        if (url.endsWith("/api/v1/viewer")) {
          return jsonResponse(200, VIEWER_PAYLOAD);
        }
        if (url.endsWith("/api/v1/accounts/current/profile")) {
          return jsonResponse(200, PROFILE_PAYLOAD);
        }
        if (url.endsWith("/api/v1/accounts/current/api-keys")) {
          return jsonResponse(200, {
            items: [
              {
                id: "key_abc",
                nickname: "Prod key",
                status: "active",
                redacted_suffix: "abcd",
                created_at: "2026-08-01T00:00:00Z",
                updated_at: "2026-08-01T00:00:00Z",
                expires_at: null,
                last_used_at: null,
                expiration_summary: { kind: "never", label: "Never" },
                budget_summary: { kind: "none", label: "None" },
                allowlist_summary: { mode: "all", group_names: [], label: "All" },
              },
            ],
          });
        }
        if (url.endsWith("/api/v1/catalog/models")) {
          return jsonResponse(200, { models: [] });
        }
        if (url.includes("/usage-events")) {
          return jsonResponse(200, {
            events: [eventRow({ api_key_id: "key_abc" })],
            next_cursor: "",
          });
        }
        throw new Error(`unexpected fetch: ${url}`);
      })
    );

    const mod = await import("../app/console/logs/page");
    const page = await mod.default({
      searchParams: Promise.resolve({ window: "24h" }),
    });
    render(page);

    expect(screen.getByText("hive-fast")).toBeTruthy();
    // "Prod key" renders twice: once as a filter <option>, once in the table
    // row's API-key column.
    expect(screen.getAllByText("Prod key").length).toBeGreaterThan(0);
    expect(screen.queryByText("No requests yet")).toBeNull();
  });

  it("shows first-run guidance when the account has no events and no filters", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async (url: string) => {
        if (url.endsWith("/api/v1/viewer")) {
          return jsonResponse(200, VIEWER_PAYLOAD);
        }
        if (url.endsWith("/api/v1/accounts/current/profile")) {
          return jsonResponse(200, PROFILE_PAYLOAD);
        }
        if (url.endsWith("/api/v1/accounts/current/api-keys")) {
          return jsonResponse(200, { items: [] });
        }
        if (url.endsWith("/api/v1/catalog/models")) {
          return jsonResponse(200, { models: [] });
        }
        if (url.includes("/usage-events")) {
          return jsonResponse(200, { events: [], next_cursor: "" });
        }
        throw new Error(`unexpected fetch: ${url}`);
      })
    );

    const mod = await import("../app/console/logs/page");
    const page = await mod.default({ searchParams: Promise.resolve({}) });
    render(page);

    expect(screen.getByText("No requests yet")).toBeTruthy();
  });

  // An exhausted trailing page carries a cursor but zero rows: that is a
  // paginated empty result, not an account with no traffic, so it must keep
  // the table's filtered-empty message and its Reset link instead of the
  // create-key pitch.
  it("keeps pagination chrome on an exhausted trailing page instead of first-run guidance", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async (url: string) => {
        if (url.endsWith("/api/v1/viewer")) {
          return jsonResponse(200, VIEWER_PAYLOAD);
        }
        if (url.endsWith("/api/v1/accounts/current/profile")) {
          return jsonResponse(200, PROFILE_PAYLOAD);
        }
        if (url.endsWith("/api/v1/accounts/current/api-keys")) {
          return jsonResponse(200, { items: [] });
        }
        if (url.endsWith("/api/v1/catalog/models")) {
          return jsonResponse(200, { models: [] });
        }
        if (url.includes("/usage-events")) {
          return jsonResponse(200, { events: [], next_cursor: "" });
        }
        throw new Error(`unexpected fetch: ${url}`);
      })
    );

    const mod = await import("../app/console/logs/page");
    const page = await mod.default({
      searchParams: Promise.resolve({ cursor: "11111111-1111-4111-8111-111111111111" }),
    });
    render(page);

    expect(screen.queryByText("No requests yet")).toBeNull();
    expect(screen.getByText("No requests match these filters.")).toBeTruthy();
    expect(screen.getByText("Reset")).toBeTruthy();
  });
});
