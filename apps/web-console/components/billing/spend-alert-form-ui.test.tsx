/**
 * Behavioral UI tests for the spend-alert creation surface: SpendAlertForm
 * submit validation and the BudgetAlertBanner dismiss (the alert delete
 * path a user can actually trigger; the per-alert DELETE proxy has no
 * console control yet).
 */
import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";

import { SpendAlertForm } from "./spend-alert-form";
import { BudgetAlertBanner } from "./budget-alert-banner";
import type { BudgetThreshold } from "@/lib/control-plane/client";

const ALERTS_URL = (ws: string) => `/api/spend-alerts/${ws}`;
const BUDGET_URL = "/api/budget";

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

function thresholdInput(): HTMLSelectElement {
  const el = screen.getByLabelText(/threshold/i);
  if (!(el instanceof HTMLSelectElement)) {
    throw new Error("threshold select not found");
  }
  return el;
}

function emailInput(): HTMLInputElement {
  const el = screen.getByLabelText(/notify email/i);
  if (!(el instanceof HTMLInputElement)) {
    throw new Error("email input not found");
  }
  return el;
}

function webhookInput(): HTMLInputElement {
  const el = screen.getByLabelText(/webhook url/i);
  if (!(el instanceof HTMLInputElement)) {
    throw new Error("webhook input not found");
  }
  return el;
}

describe("SpendAlertForm behavior", () => {
  it("submit fires once with the selected threshold and only the filled channel", async () => {
    const fetchMock = vi.fn().mockResolvedValue(
      new Response(JSON.stringify({ id: "al-1" }), { status: 201 }),
    );
    vi.stubGlobal("fetch", fetchMock);

    render(
      <SpendAlertForm workspaceId="ws-1" readOnly={false} existingThresholds={[80]} />,
    );
    fireEvent.change(thresholdInput(), { target: { value: "50" } });
    fireEvent.change(emailInput(), { target: { value: "alerts@example.test" } });
    fireEvent.click(screen.getByRole("button", { name: /create alert/i }));

    await waitFor(() => {
      expect(fetchMock).toHaveBeenCalledTimes(1);
    });
    const [url, init] = fetchMock.mock.calls[0];
    expect(String(url)).toBe(ALERTS_URL("ws-1"));
    expect(init?.method).toBe("POST");
    expect(JSON.parse(String(init?.body))).toEqual({
      threshold_pct: 50,
      email: "alerts@example.test",
      webhook_url: null,
    });
    expect(await screen.findByRole("status")).toBeTruthy();
  });

  it("webhook-only alerts post a null email and the chosen threshold", async () => {
    const fetchMock = vi.fn().mockResolvedValue(
      new Response(JSON.stringify({ id: "al-2" }), { status: 201 }),
    );
    vi.stubGlobal("fetch", fetchMock);

    render(
      <SpendAlertForm workspaceId="ws-1" readOnly={false} existingThresholds={[]} />,
    );
    fireEvent.change(thresholdInput(), { target: { value: "100" } });
    fireEvent.change(webhookInput(), {
      target: { value: "https://hooks.example.test/spend" },
    });
    fireEvent.click(screen.getByRole("button", { name: /create alert/i }));

    await waitFor(() => {
      expect(fetchMock).toHaveBeenCalledTimes(1);
    });
    const body = JSON.parse(String(fetchMock.mock.calls[0][1]?.body));
    expect(body).toEqual({
      threshold_pct: 100,
      email: null,
      webhook_url: "https://hooks.example.test/spend",
    });
  });

  it("an already-configured threshold is refused client side without a request", async () => {
    const fetchMock = vi.fn();
    vi.stubGlobal("fetch", fetchMock);

    render(
      <SpendAlertForm workspaceId="ws-1" readOnly={false} existingThresholds={[50]} />,
    );
    fireEvent.change(emailInput(), { target: { value: "alerts@example.test" } });
    fireEvent.click(screen.getByRole("button", { name: /create alert/i }));

    const alert = await screen.findByRole("alert");
    expect(alert.textContent).toContain("already exists");
    expect(fetchMock).not.toHaveBeenCalled();
  });

  it("no delivery channel at all is refused without a request", async () => {
    const fetchMock = vi.fn();
    vi.stubGlobal("fetch", fetchMock);

    render(
      <SpendAlertForm workspaceId="ws-1" readOnly={false} existingThresholds={[]} />,
    );
    fireEvent.click(screen.getByRole("button", { name: /create alert/i }));

    const alert = await screen.findByRole("alert");
    expect(alert.textContent).toContain("at least one delivery channel");
    expect(fetchMock).not.toHaveBeenCalled();
  });

  it("read-only mode disables every control and blocks submission", async () => {
    const fetchMock = vi.fn();
    vi.stubGlobal("fetch", fetchMock);

    render(
      <SpendAlertForm workspaceId="ws-1" readOnly existingThresholds={[]} />,
    );
    expect(thresholdInput().disabled).toBe(true);
    expect(emailInput().disabled).toBe(true);
    expect(webhookInput().disabled).toBe(true);
    const create = screen.getByRole("button", { name: /create alert/i });
    expect(create instanceof HTMLButtonElement && create.disabled).toBe(true);

    const form = create.closest("form");
    if (!form) {
      throw new Error("spend alert form not found");
    }
    fireEvent.submit(form);
    const alert = await screen.findByRole("alert");
    expect(alert.textContent).toContain("Only the workspace owner");
    expect(fetchMock).not.toHaveBeenCalled();
  });

  it("server rejection surfaces the backend message and keeps the form editable", async () => {
    const fetchMock = vi.fn().mockResolvedValue(
      new Response(JSON.stringify({ error: "email looks invalid" }), { status: 400 }),
    );
    vi.stubGlobal("fetch", fetchMock);

    render(
      <SpendAlertForm workspaceId="ws-1" readOnly={false} existingThresholds={[]} />,
    );
    fireEvent.change(emailInput(), { target: { value: "alerts@example.test" } });
    fireEvent.click(screen.getByRole("button", { name: /create alert/i }));

    const alert = await screen.findByRole("alert");
    expect(alert.textContent).toContain("email looks invalid");
  });

  it("success clears both channels so a second submit cannot silently duplicate", async () => {
    const fetchMock = vi.fn().mockResolvedValue(
      new Response(JSON.stringify({ id: "al-3" }), { status: 201 }),
    );
    vi.stubGlobal("fetch", fetchMock);

    render(
      <SpendAlertForm workspaceId="ws-1" readOnly={false} existingThresholds={[]} />,
    );
    fireEvent.change(emailInput(), { target: { value: "alerts@example.test" } });
    fireEvent.click(screen.getByRole("button", { name: /create alert/i }));
    await screen.findByRole("status");

    expect(emailInput().value).toBe("");
    expect(webhookInput().value).toBe("");

    // The immediate resubmit now hits the no-channel guard, not the API.
    fireEvent.click(screen.getByRole("button", { name: /create alert/i }));
    await screen.findByRole("alert");
    expect(fetchMock).toHaveBeenCalledTimes(1);
  });
});

describe("BudgetAlertBanner dismiss (alert delete path)", () => {
  function thresholdFixture(): BudgetThreshold {
    return {
      id: "th-1",
      threshold_credits: 500,
      alert_dismissed: false,
      last_notified_at: null,
      created_at: "2026-08-01T00:00:00Z",
      updated_at: "2026-08-01T00:00:00Z",
    };
  }

  it("renders nothing while the balance is comfortably above the threshold", () => {
    const { container } = render(
      <BudgetAlertBanner threshold={thresholdFixture()} currentBalance={10_000} />,
    );
    expect(container.childElementCount).toBe(0);
  });

  it("dismiss fires the budget DELETE once and hides the banner", async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response("{}", { status: 200 }));
    vi.stubGlobal("fetch", fetchMock);

    render(
      <BudgetAlertBanner
        threshold={thresholdFixture()}
        currentBalance={300}
      />,
    );
    fireEvent.click(screen.getByRole("button", { name: /dismiss/i }));

    await waitFor(() => {
      expect(fetchMock).toHaveBeenCalledTimes(1);
    });
    const [url, init] = fetchMock.mock.calls[0];
    expect(String(url)).toBe(BUDGET_URL);
    expect(init?.method).toBe("DELETE");
    await waitFor(() => {
      expect(screen.queryByRole("status")).toBeNull();
    });
  });

  it("dismiss hides the banner even when the network call fails", async () => {
    const fetchMock = vi.fn().mockRejectedValue(new TypeError("offline"));
    vi.stubGlobal("fetch", fetchMock);

    render(
      <BudgetAlertBanner
        threshold={thresholdFixture()}
        currentBalance={300}
      />,
    );
    fireEvent.click(screen.getByRole("button", { name: /dismiss/i }));

    await waitFor(() => {
      expect(screen.queryByRole("status")).toBeNull();
    });
  });
});
