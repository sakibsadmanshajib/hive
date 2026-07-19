import { describe, it, expect, vi, afterEach } from "vitest";
import { render, screen, fireEvent, waitFor, cleanup } from "@testing-library/react";

import { FeatureGateManager } from "./feature-gate-manager";
import type { FeatureGate } from "@/lib/control-plane/client";

const GATES: FeatureGate[] = [
  { key: "ENABLE_RAG", label: "Agent RAG capability", category: "agents", enabled: false },
  { key: "ENABLE_PUBLIC_BILLING", label: "Public billing", category: "billing", enabled: true },
];

describe("FeatureGateManager", () => {
  afterEach(() => {
    cleanup();
    vi.restoreAllMocks();
  });

  it("renders each gate label, raw key, and grouped category headings", () => {
    render(<FeatureGateManager gates={GATES} />);
    // getByText throws if absent, so the calls themselves assert presence.
    expect(screen.getByText("Agent RAG capability")).toBeTruthy();
    expect(screen.getByText("ENABLE_RAG")).toBeTruthy();
    expect(screen.getByText("Sovereign workspace")).toBeTruthy();
    expect(screen.getByText("Billing & payments")).toBeTruthy();
  });

  it("reflects initial enabled state on the switches", () => {
    render(<FeatureGateManager gates={GATES} />);
    const rag = screen.getByRole("switch", { name: /Agent RAG capability/i });
    const billing = screen.getByRole("switch", { name: /Public billing/i });
    expect(rag.getAttribute("aria-checked")).toBe("false");
    expect(billing.getAttribute("aria-checked")).toBe("true");
  });

  it("optimistically flips and PUTs to the BFF route on toggle", async () => {
    const fetchMock = vi.fn().mockResolvedValue(
      new Response(JSON.stringify({ key: "ENABLE_RAG", enabled: true }), {
        status: 200,
      }),
    );
    vi.stubGlobal("fetch", fetchMock);

    render(<FeatureGateManager gates={GATES} />);
    const rag = screen.getByRole("switch", { name: /Agent RAG capability/i });
    fireEvent.click(rag);

    await waitFor(() => {
      expect(rag.getAttribute("aria-checked")).toBe("true");
    });
    expect(fetchMock).toHaveBeenCalledTimes(1);
    const call = fetchMock.mock.calls[0];
    expect(call[0]).toBe("/api/console/feature-gates");
    const init = call[1];
    expect(init?.method).toBe("PUT");
    expect(JSON.parse(String(init?.body))).toEqual({
      key: "ENABLE_RAG",
      enabled: true,
    });
  });

  it("reverts the switch and shows an error when the request fails", async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response("nope", { status: 500 }));
    vi.stubGlobal("fetch", fetchMock);

    render(<FeatureGateManager gates={GATES} />);
    const rag = screen.getByRole("switch", { name: /Agent RAG capability/i });
    fireEvent.click(rag);

    await waitFor(() => {
      expect(screen.getByText(/Could not save/i)).toBeTruthy();
    });
    expect(rag.getAttribute("aria-checked")).toBe("false");
  });
});
