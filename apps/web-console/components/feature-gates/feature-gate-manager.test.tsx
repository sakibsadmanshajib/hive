import { describe, it, expect, vi, afterEach } from "vitest";
import { render, screen, fireEvent, waitFor, cleanup } from "@testing-library/react";

import { FeatureGateManager } from "./feature-gate-manager";
import type { FeatureGate } from "@/lib/control-plane/client";

const GATES: FeatureGate[] = [
  { key: "ENABLE_RAG", label: "Agent RAG capability", category: "agents", enabled: false, manageable: true },
  { key: "ENABLE_PUBLIC_BILLING", label: "Public billing", category: "billing", enabled: true, manageable: true },
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
  // Issue #755. The route computes a specific message per status and the
  // component used to throw that body away, so every failure read "Could not
  // save. Try again." A key the registry no longer carries answers 400
  // permanently, and telling an operator to retry something that cannot
  // succeed is the same class of defect as the toggle that lied.
  it("shows the server's message when the request fails with one", async () => {
    const fetchMock = vi.fn().mockResolvedValue(
      new Response(JSON.stringify({ error: "That feature gate is not recognized." }), {
        status: 400,
      }),
    );
    vi.stubGlobal("fetch", fetchMock);

    render(<FeatureGateManager gates={GATES} />);
    const rag = screen.getByRole("switch", { name: /Agent RAG capability/i });
    fireEvent.click(rag);

    await waitFor(() => {
      expect(screen.getByText("That feature gate is not recognized.")).toBeTruthy();
    });
    expect(screen.queryByText(/Could not save/i)).toBeNull();
    expect(rag.getAttribute("aria-checked")).toBe("false");
  });

  // Issue #758: a workspace owner reads the platform-managed gates but does not
  // get a control the control-plane would refuse.
  it("renders a platform-managed gate read-only", () => {
    render(
      <FeatureGateManager
        gates={[
          {
            key: "ENABLE_EXTRA_USAGE",
            label: "Extra usage beyond plan",
            category: "billing",
            enabled: false,
            manageable: false,
          },
        ]}
      />,
    );
    expect(
      screen.queryByRole("switch", { name: /Extra usage beyond plan/i }),
    ).toBeNull();
    expect(screen.getByText("Managed by your administrator")).toBeTruthy();
  });
});
