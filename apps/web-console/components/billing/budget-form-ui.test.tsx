/**
 * Behavioral UI tests for BudgetForm: the workspace budget cap editor.
 * The sibling budget-form.test.tsx locks the taka/subunit conversion math;
 * this suite proves what a user's submit actually sends and what invalid
 * input refuses to send.
 */
import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";

import { BudgetForm } from "./budget-form";
import type { BudgetSettings } from "@/lib/control-plane/client";

const BUDGET_URL = (ws: string) => `/api/budget/${ws}`;

function settingsFixture(overrides: Partial<BudgetSettings> = {}): BudgetSettings {
  return {
    workspace_id: "ws-1",
    period_start: "2026-08-01",
    soft_cap_bdt_subunits: 100_000,
    hard_cap_bdt_subunits: 200_000,
    currency: "BDT",
    created_at: "2026-08-01T00:00:00Z",
    updated_at: "2026-08-01T00:00:00Z",
    ...overrides,
  };
}

function inputById(id: string): HTMLInputElement {
  const el = screen.getByLabelText(new RegExp(id.replace("budget-", ""), "i"));
  if (!(el instanceof HTMLInputElement)) {
    throw new Error(`input ${id} not found`);
  }
  return el;
}

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

describe("BudgetForm behavior", () => {
  it("prefills both caps from existing settings as editable taka strings", () => {
    render(<BudgetForm workspaceId="ws-1" budget={settingsFixture()} readOnly={false} />);
    const soft = inputById("soft");
    const hard = inputById("hard");
    expect(soft.value).toBe("1000.00");
    expect(hard.value).toBe("2000.00");
    expect(screen.queryByRole("alert")).toBeNull();
  });

  it("submit fires once with parsed subunit values", async () => {
    const fetchMock = vi.fn().mockResolvedValue(
      new Response(JSON.stringify(settingsFixture()), { status: 200 }),
    );
    vi.stubGlobal("fetch", fetchMock);

    render(<BudgetForm workspaceId="ws-1" budget={null} readOnly={false} />);
    fireEvent.change(inputById("soft"), { target: { value: "10.50" } });
    fireEvent.change(inputById("hard"), { target: { value: "20" } });
    fireEvent.click(screen.getByRole("button", { name: /save budget/i }));

    await waitFor(() => {
      expect(fetchMock).toHaveBeenCalledTimes(1);
    });
    const [url, init] = fetchMock.mock.calls[0];
    expect(String(url)).toBe(BUDGET_URL("ws-1"));
    expect(init?.method).toBe("PUT");
    expect(JSON.parse(String(init?.body))).toEqual({
      soft_cap_bdt_subunits: 1050,
      hard_cap_bdt_subunits: 2000,
    });
    expect(await screen.findByRole("status")).toBeTruthy();
  });

  it("malformed caps are refused client side without any network call", async () => {
    const fetchMock = vi.fn();
    vi.stubGlobal("fetch", fetchMock);

    render(<BudgetForm workspaceId="ws-1" budget={null} readOnly={false} />);
    fireEvent.change(inputById("soft"), { target: { value: "abc" } });
    fireEvent.change(inputById("hard"), { target: { value: "-5" } });
    fireEvent.click(screen.getByRole("button", { name: /save budget/i }));

    const alert = await screen.findByRole("alert");
    expect(alert.textContent).toContain("non-negative numbers");
    expect(fetchMock).not.toHaveBeenCalled();
  });

  it("a soft cap above the hard cap never reaches the server", async () => {
    const fetchMock = vi.fn();
    vi.stubGlobal("fetch", fetchMock);

    render(<BudgetForm workspaceId="ws-1" budget={null} readOnly={false} />);
    fireEvent.change(inputById("soft"), { target: { value: "50" } });
    fireEvent.change(inputById("hard"), { target: { value: "40" } });
    fireEvent.click(screen.getByRole("button", { name: /save budget/i }));

    const alert = await screen.findByRole("alert");
    expect(alert.textContent).toContain("less than or equal to hard cap");
    expect(fetchMock).not.toHaveBeenCalled();
  });

  it("read-only mode disables the form and blocks submission", async () => {
    const fetchMock = vi.fn();
    vi.stubGlobal("fetch", fetchMock);

    render(
      <BudgetForm
        workspaceId="ws-1"
        budget={settingsFixture()}
        readOnly
      />,
    );
    expect(inputById("soft").disabled).toBe(true);
    expect(inputById("hard").disabled).toBe(true);
    const save = screen.getByRole("button", { name: /save budget/i });
    expect(save instanceof HTMLButtonElement && save.disabled).toBe(true);

    // Even a forced submit event is refused.
    const form = save.closest("form");
    if (!form) {
      throw new Error("budget form not found");
    }
    fireEvent.submit(form);
    const alert = await screen.findByRole("alert");
    expect(alert.textContent).toContain("Only the workspace owner");
    expect(fetchMock).not.toHaveBeenCalled();
  });

  it("server rejection surfaces the backend error message", async () => {
    const fetchMock = vi.fn().mockResolvedValue(
      new Response(JSON.stringify({ error: "hard cap exceeds account limit" }), { status: 422 }),
    );
    vi.stubGlobal("fetch", fetchMock);

    render(<BudgetForm workspaceId="ws-1" budget={null} readOnly={false} />);
    fireEvent.change(inputById("soft"), { target: { value: "10" } });
    fireEvent.change(inputById("hard"), { target: { value: "20" } });
    fireEvent.click(screen.getByRole("button", { name: /save budget/i }));

    const alert = await screen.findByRole("alert");
    expect(alert.textContent).toContain("hard cap exceeds account limit");
  });

  it("network failure falls back to a retryable message", async () => {
    const fetchMock = vi.fn().mockRejectedValue(new TypeError("connection lost"));
    vi.stubGlobal("fetch", fetchMock);

    render(<BudgetForm workspaceId="ws-1" budget={null} readOnly={false} />);
    fireEvent.change(inputById("soft"), { target: { value: "1" } });
    fireEvent.change(inputById("hard"), { target: { value: "2" } });
    fireEvent.click(screen.getByRole("button", { name: /save budget/i }));

    const alert = await screen.findByRole("alert");
    expect(alert.textContent).toContain("connection lost");
  });
});
