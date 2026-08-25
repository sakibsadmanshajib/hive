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
