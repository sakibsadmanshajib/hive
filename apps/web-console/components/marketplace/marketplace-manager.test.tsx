import { describe, it, expect, vi, afterEach } from "vitest";
import { render, screen, fireEvent, waitFor, cleanup } from "@testing-library/react";

import { MarketplaceManager } from "./marketplace-manager";
import type { MarketplaceEntry } from "@/lib/control-plane/client";

function entry(overrides: Partial<MarketplaceEntry> = {}): MarketplaceEntry {
  return {
    id: "11111111-1111-1111-1111-111111111111",
    kind: "mcp_server",
    name: "github",
    description: "GitHub MCP server",
    config: { command: "npx" },
    enabled: false,
    created_at: "2026-07-16T00:00:00Z",
    updated_at: "2026-07-16T00:00:00Z",
    ...overrides,
  };
}

const ENTRIES: MarketplaceEntry[] = [
  entry(),
  entry({
    id: "22222222-2222-2222-2222-222222222222",
    kind: "skill",
    name: "deck-writer",
    description: "Slide deck generation skill",
    config: {},
    enabled: true,
  }),
];

describe("MarketplaceManager", () => {
  afterEach(() => {
    cleanup();
    vi.restoreAllMocks();
  });

  it("renders each entry name, description, and grouped kind headings", () => {
    render(<MarketplaceManager entries={ENTRIES} />);
    expect(screen.getByText("github")).toBeTruthy();
    expect(screen.getByText("GitHub MCP server")).toBeTruthy();
    expect(screen.getByText("MCP servers")).toBeTruthy();
    expect(screen.getByText("Skills")).toBeTruthy();
  });

  it("reflects initial enabled state on the switches", () => {
    render(<MarketplaceManager entries={ENTRIES} />);
    const github = screen.getByRole("switch", { name: /github: disabled/i });
    const deck = screen.getByRole("switch", { name: /deck-writer: enabled/i });
    expect(github.getAttribute("aria-checked")).toBe("false");
    expect(deck.getAttribute("aria-checked")).toBe("true");
  });

  it("optimistically flips and PUTs to the BFF enable route on toggle", async () => {
    const fetchMock = vi.fn().mockResolvedValue(
      new Response(JSON.stringify({ id: ENTRIES[0].id, enabled: true }), { status: 200 }),
    );
    vi.stubGlobal("fetch", fetchMock);

    render(<MarketplaceManager entries={ENTRIES} />);
    const github = screen.getByRole("switch", { name: /github: disabled/i });
    fireEvent.click(github);

    await waitFor(() => {
      expect(github.getAttribute("aria-checked")).toBe("true");
    });
    expect(fetchMock).toHaveBeenCalledTimes(1);
    const call = fetchMock.mock.calls[0];
    expect(call[0]).toBe(`/api/console/marketplace/${ENTRIES[0].id}/enable`);
    const init = call[1];
    expect(init?.method).toBe("PUT");
    expect(JSON.parse(String(init?.body))).toEqual({ enabled: true });
  });

  it("reverts the switch and shows an error when the enable request fails", async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response("nope", { status: 500 }));
    vi.stubGlobal("fetch", fetchMock);

    render(<MarketplaceManager entries={ENTRIES} />);
    const github = screen.getByRole("switch", { name: /github: disabled/i });
    fireEvent.click(github);

    await waitFor(() => {
      expect(screen.getByText(/Could not save/i)).toBeTruthy();
    });
    expect(github.getAttribute("aria-checked")).toBe("false");
  });

  it("removes the row after a successful delete", async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response(JSON.stringify({ status: "deleted" }), { status: 200 }));
    vi.stubGlobal("fetch", fetchMock);

    render(<MarketplaceManager entries={ENTRIES} />);
    const deleteButton = screen.getByRole("button", { name: /delete github/i });
    fireEvent.click(deleteButton);

    await waitFor(() => {
      expect(screen.queryByText("github")).toBeNull();
    });
    const call = fetchMock.mock.calls[0];
    expect(call[0]).toBe(`/api/console/marketplace/${ENTRIES[0].id}`);
    expect(call[1]?.method).toBe("DELETE");
  });

  it("curates a new entry and adds it to the list on submit", async () => {
    const created: MarketplaceEntry = entry({
      id: "33333333-3333-3333-3333-333333333333",
      kind: "mcp_server",
      name: "slack",
      description: "Slack MCP server",
      config: { url: "https://mcp.example.invalid" },
      enabled: false,
    });
    const fetchMock = vi.fn().mockResolvedValue(new Response(JSON.stringify(created), { status: 201 }));
    vi.stubGlobal("fetch", fetchMock);

    render(<MarketplaceManager entries={[]} />);
    fireEvent.change(screen.getByPlaceholderText("Name (e.g. github)"), {
      target: { value: "slack" },
    });
    fireEvent.click(screen.getByRole("button", { name: /curate entry/i }));

    await waitFor(() => {
      expect(screen.getByText("slack")).toBeTruthy();
    });
    const call = fetchMock.mock.calls[0];
    expect(call[0]).toBe("/api/console/marketplace");
    expect(call[1]?.method).toBe("POST");
    expect(JSON.parse(String(call[1]?.body))).toMatchObject({ kind: "mcp_server", name: "slack" });
  });

  it("shows a form error and does not submit when config is invalid JSON", async () => {
    const fetchMock = vi.fn();
    vi.stubGlobal("fetch", fetchMock);

    render(<MarketplaceManager entries={[]} />);
    fireEvent.change(screen.getByPlaceholderText("Name (e.g. github)"), {
      target: { value: "github" },
    });
    fireEvent.change(screen.getByPlaceholderText(/"command":"npx"/), {
      target: { value: "not json" },
    });
    fireEvent.click(screen.getByRole("button", { name: /curate entry/i }));

    await waitFor(() => {
      expect(screen.getByText(/Config must be valid JSON/i)).toBeTruthy();
    });
    expect(fetchMock).not.toHaveBeenCalled();
  });
});
