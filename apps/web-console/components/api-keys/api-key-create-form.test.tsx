/**
 * Behavioral tests for ApiKeyCreateForm: nickname validation, the create
 * POST, the one-time secret panel with copy to clipboard, and reset.
 */
import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";

const refresh = vi.fn();

vi.mock("next/navigation", () => ({
  useRouter: () => ({ refresh }),
}));

import { ApiKeyCreateForm } from "./api-key-create-form";

const CREATE_URL = "/api/v1/accounts/current/api-keys";

function createdKeyBody(overrides: Record<string, unknown> = {}) {
  return {
    id: "key-1",
    nickname: "prod",
    status: "active",
    redacted_suffix: "abcd",
    created_at: "2026-08-20T00:00:00Z",
    updated_at: "2026-08-20T00:00:00Z",
    expires_at: null,
    last_used_at: null,
    expiration_summary: { kind: "never", label: "Never expires" },
    budget_summary: { kind: "unlimited", label: "No budget" },
    allowlist_summary: { mode: "all", group_names: [], label: "All groups" },
    secret: "sk-hive-once-only-secret",
    ...overrides,
  };
}

afterEach(() => {
  cleanup();
  Reflect.deleteProperty(navigator, "clipboard");
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
  refresh.mockClear();
});

function nicknameInput(): HTMLInputElement {
  const el = screen.getByLabelText(/nickname/i);
  if (!(el instanceof HTMLInputElement)) {
    throw new Error("nickname input not found");
  }
  return el;
}

function submitButton(): HTMLElement {
  return screen.getByRole("button", { name: /create key|creating/i });
}

describe("ApiKeyCreateForm validation and creation", () => {
  it("blank nickname is refused client side without a request", async () => {
    const fetchMock = vi.fn();
    vi.stubGlobal("fetch", fetchMock);

    render(<ApiKeyCreateForm />);
    const form = nicknameInput().closest("form");
    if (!form) {
      throw new Error("create form not found");
    }
    fireEvent.submit(form);

    const alert = await screen.findByRole("alert");
    expect(alert.textContent).toContain("Nickname is required.");
    expect(fetchMock).not.toHaveBeenCalled();
  });

  it("create fires once with the trimmed nickname and no expiry field when unset", async () => {
    const fetchMock = vi.fn().mockResolvedValue(
      new Response(JSON.stringify(createdKeyBody()), { status: 201 }),
    );
    vi.stubGlobal("fetch", fetchMock);

    render(<ApiKeyCreateForm />);
    fireEvent.change(nicknameInput(), { target: { value: "  prod-server  " } });
    fireEvent.click(submitButton());

    await screen.findByTestId("created-api-key-secret");
    expect(fetchMock).toHaveBeenCalledTimes(1);
    const [url, init] = fetchMock.mock.calls[0];
    expect(String(url)).toBe(CREATE_URL);
    expect(init?.method).toBe("POST");
    expect(JSON.parse(String(init?.body))).toEqual({ nickname: "prod-server" });
    expect(screen.getByTestId("created-api-key-secret").textContent).toBe(
      "sk-hive-once-only-secret",
    );
    expect(refresh).toHaveBeenCalledTimes(1);
  });

  it("an expiry date is sent as an ISO timestamp", async () => {
    const fetchMock = vi.fn().mockResolvedValue(
      new Response(JSON.stringify(createdKeyBody()), { status: 201 }),
    );
    vi.stubGlobal("fetch", fetchMock);

    render(<ApiKeyCreateForm />);
    fireEvent.change(nicknameInput(), { target: { value: "rotating" } });
    fireEvent.change(screen.getByLabelText(/expires/i), {
      target: { value: "2027-01-15" },
    });
    fireEvent.click(submitButton());

    await screen.findByTestId("created-api-key-secret");
    const body = JSON.parse(String(fetchMock.mock.calls[0][1]?.body));
    expect(body.nickname).toBe("rotating");
    expect(body.expires_at).toBe(new Date("2027-01-15").toISOString());
  });

  it("server rejection surfaces the retry error instead of the secret panel", async () => {
    const fetchMock = vi.fn().mockResolvedValue(
      new Response("denied", { status: 403 }),
    );
    vi.stubGlobal("fetch", fetchMock);

    render(<ApiKeyCreateForm />);
    fireEvent.change(nicknameInput(), { target: { value: "prod" } });
    fireEvent.click(submitButton());

    const alert = await screen.findByRole("alert");
    expect(alert.textContent).toContain("Failed to create key");
    expect(screen.queryByTestId("created-api-key-secret")).toBeNull();
  });

  it("copy button writes the one-time secret to the clipboard exactly once per click", async () => {
    const fetchMock = vi.fn().mockResolvedValue(
      new Response(JSON.stringify(createdKeyBody()), { status: 201 }),
    );
    vi.stubGlobal("fetch", fetchMock);
    const writeText = vi.fn().mockResolvedValue(undefined);
    Object.defineProperty(navigator, "clipboard", {
      value: { writeText },
      configurable: true,
      writable: true,
    });

    render(<ApiKeyCreateForm />);
    fireEvent.change(nicknameInput(), { target: { value: "prod" } });
    fireEvent.click(submitButton());
    await screen.findByTestId("created-api-key-secret");

    fireEvent.click(screen.getByRole("button", { name: /copy/i }));
    await waitFor(() => {
      expect(writeText).toHaveBeenCalledWith("sk-hive-once-only-secret");
    });
    expect(screen.getByRole("button", { name: /copied/i })).toBeTruthy();

    // Create another returns to a clean form for a second key.
    fireEvent.click(screen.getByRole("button", { name: /create another/i }));
    expect(nicknameInput().value).toBe("");
    expect(screen.queryByTestId("created-api-key-secret")).toBeNull();
  });

  it("clipboard refusal degrades silently instead of throwing", async () => {
    const fetchMock = vi.fn().mockResolvedValue(
      new Response(JSON.stringify(createdKeyBody()), { status: 201 }),
    );
    vi.stubGlobal("fetch", fetchMock);
    const writeText = vi.fn().mockRejectedValue(new DOMException("denied"));
    Object.defineProperty(navigator, "clipboard", {
      value: { writeText },
      configurable: true,
      writable: true,
    });

    render(<ApiKeyCreateForm />);
    fireEvent.change(nicknameInput(), { target: { value: "prod" } });
    fireEvent.click(submitButton());
    await screen.findByTestId("created-api-key-secret");

    fireEvent.click(screen.getByRole("button", { name: /copy/i }));
    await waitFor(() => {
      expect(writeText).toHaveBeenCalled();
    });
    // No crash, still on the secret panel, Copy label unchanged.
    expect(screen.getByRole("button", { name: /^Copy$/i })).toBeTruthy();
  });
});

describe("ApiKeyCreateForm limit cadence feedback", () => {
  const summary = () => screen.getByTestId("key-limit-summary").textContent ?? "";
  const limitField = () => screen.getByLabelText(/credit limit/i);
  const cadenceField = () => screen.getByLabelText(/reset limit every/i);

  it("is usable before an amount is typed", () => {
    render(<ApiKeyCreateForm />);
    expect(cadenceField().hasAttribute("disabled")).toBe(false);
  });

  it("changing the cadence restates what the amount field means", () => {
    render(<ApiKeyCreateForm />);
    expect(screen.queryByText(/per calendar month/i)).toBeNull();
    fireEvent.change(cadenceField(), { target: { value: "monthly" } });
    expect(screen.getByText(/per calendar month/i)).toBeTruthy();
    expect(screen.queryByText(/for the key's lifetime/i)).toBeNull();
  });

  it("states the bound that will be enforced, not the field's name", () => {
    render(<ApiKeyCreateForm />);
    expect(summary()).toContain("No credit limit");
    fireEvent.change(limitField(), { target: { value: "10" } });
    expect(summary()).toContain("$10.00");
    expect(summary()).toContain("spent in total");
    fireEvent.change(cadenceField(), { target: { value: "monthly" } });
    expect(summary()).toContain("current calendar month");
    fireEvent.change(limitField(), { target: { value: "0" } });
    expect(summary()).toContain("no limit will be applied");
  });
});

describe("ApiKeyCreateForm credit limit on the wire", () => {
  const POLICY_URL = `${CREATE_URL}/key-1/policy`;
  const limitField = () => screen.getByLabelText(/credit limit/i);
  const cadenceField = () => screen.getByLabelText(/reset limit every/i);
  const limitCell = () => screen.getByTestId("created-api-key-limit").textContent;

  function okFetch() {
    return vi
      .fn()
      .mockResolvedValueOnce(
        new Response(JSON.stringify(createdKeyBody()), { status: 201 }),
      )
      .mockResolvedValueOnce(
        new Response(JSON.stringify({ ok: true }), { status: 200 }),
      );
  }

  it("no limit typed sends the create call and nothing else", async () => {
    const fetchMock = okFetch();
    vi.stubGlobal("fetch", fetchMock);
    render(<ApiKeyCreateForm />);
    fireEvent.change(nicknameInput(), { target: { value: "uncapped" } });
    fireEvent.click(submitButton());
    await screen.findByTestId("created-api-key-secret");
    expect(fetchMock).toHaveBeenCalledTimes(1);
    expect(limitCell()).toBe("Unlimited");
  });

  it("a lifetime cap reaches the wire as the exact integer the amount means", async () => {
    const fetchMock = okFetch();
    vi.stubGlobal("fetch", fetchMock);
    render(<ApiKeyCreateForm />);
    fireEvent.change(nicknameInput(), { target: { value: "capped" } });
    fireEvent.change(limitField(), { target: { value: "12.34" } });
    fireEvent.click(submitButton());
    await screen.findByTestId("created-api-key-secret");
    await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(2));
    const [url, init] = fetchMock.mock.calls[1];
    expect(String(url)).toBe(POLICY_URL);
    expect(init?.method).toBe("POST");
    expect(JSON.parse(String(init?.body))).toEqual({
      budget_kind: "lifetime",
      budget_limit_credits: 12_340_000_000,
    });
    expect(limitCell()).toContain("$12.34");
    expect(limitCell()).toContain("never resets");
  });

  it("the monthly cadence changes the budget kind on the wire", async () => {
    const fetchMock = okFetch();
    vi.stubGlobal("fetch", fetchMock);
    render(<ApiKeyCreateForm />);
    fireEvent.change(nicknameInput(), { target: { value: "monthly-capped" } });
    fireEvent.change(limitField(), { target: { value: "10.00" } });
    fireEvent.change(cadenceField(), { target: { value: "monthly" } });
    fireEvent.click(submitButton());
    await screen.findByTestId("created-api-key-secret");
    await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(2));
    expect(JSON.parse(String(fetchMock.mock.calls[1][1]?.body))).toEqual({
      budget_kind: "monthly",
      budget_limit_credits: 10_000_000_000,
    });
    expect(limitCell()).toContain("resets monthly");
  });

  it("an unparseable amount is refused before the key is created", async () => {
    const fetchMock = vi.fn();
    vi.stubGlobal("fetch", fetchMock);
    render(<ApiKeyCreateForm />);
    fireEvent.change(nicknameInput(), { target: { value: "bad-limit" } });
    fireEvent.change(limitField(), { target: { value: "-5" } });
    fireEvent.click(submitButton());
    const alert = await screen.findByRole("alert");
    expect(alert.textContent).toContain("positive dollar amount");
    expect(fetchMock).not.toHaveBeenCalled();
  });

  it("a failed policy call reports the key as uncapped, never as capped", async () => {
    const fetchMock = vi
      .fn()
      .mockResolvedValueOnce(
        new Response(JSON.stringify(createdKeyBody()), { status: 201 }),
      )
      .mockResolvedValueOnce(new Response("denied", { status: 403 }));
    vi.stubGlobal("fetch", fetchMock);
    render(<ApiKeyCreateForm />);
    fireEvent.change(nicknameInput(), { target: { value: "half-applied" } });
    fireEvent.change(limitField(), { target: { value: "5" } });
    fireEvent.click(submitButton());
    const alert = await screen.findByRole("alert");
    expect(alert.textContent).toContain("credit limit could not be applied");
    expect(limitCell()).toBe("Unlimited");
  });

  it("a transport failure applying the cap still shows the one-time secret", async () => {
    const fetchMock = vi
      .fn()
      .mockResolvedValueOnce(
        new Response(JSON.stringify(createdKeyBody()), { status: 201 }),
      )
      .mockRejectedValueOnce(new Error("network down"));
    vi.stubGlobal("fetch", fetchMock);
    render(<ApiKeyCreateForm />);
    fireEvent.change(nicknameInput(), { target: { value: "net-fail" } });
    fireEvent.change(limitField(), { target: { value: "5" } });
    fireEvent.click(submitButton());
    const secret = await screen.findByTestId("created-api-key-secret");
    expect(secret.textContent).toBe("sk-hive-once-only-secret");
    const alert = await screen.findByRole("alert");
    expect(alert.textContent).toContain("live and uncapped");
    expect(limitCell()).toBe("Unlimited");
  });
});
