/**
 * Behavioral tests for ApiKeyCreateForm: nickname validation, the create
 * POST, the one-time secret panel with copy to clipboard, and reset.
 */
import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";

const refresh = vi.fn();

// Only the navigation calls this test drives are replaced.
// unstable_rethrow() stays real: the console's reads call it first in
// every catch so a framework throw is never classified as a data
// failure, and a stubbed one would pass whether or not that holds.
vi.mock("next/navigation", async (importOriginal) => {
  const actual = await importOriginal<typeof import("next/navigation")>();
  return {
    ...actual,
  useRouter: () => ({ refresh }),
  };
});

import { ApiKeyCreateForm } from "./api-key-create-form";
import { MAX_KEY_NICKNAME_LEN } from "@/lib/api-keys";

const CREATE_URL = "/api/v1/accounts/current/api-keys";

// Deliberately not the hosted deployment's own hostname: these assertions must
// fail if the panel ever reverts to printing a literal instead of the base URL
// this deployment was configured with (issue #550).
const formProps = {
  apiBaseUrl: "https://ai.acme-bank.internal/v1",
  quickstartModel: "hive-chat-default",
};

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
    secret: "placeholder-not-a-real-key",
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

    render(<ApiKeyCreateForm {...formProps} />);
    const form = nicknameInput().closest("form");
    if (!form) {
      throw new Error("create form not found");
    }
    fireEvent.submit(form);

    const alert = await screen.findByRole("alert");
    expect(alert.textContent).toContain("Nickname is required.");
    expect(fetchMock).not.toHaveBeenCalled();
  });

  it("caps what the nickname field will accept at all", () => {
    // Issue #1400. The browser stops typing past the cap; the two assertions
    // below cover a value that arrives some other way (a paste handler, a
    // programmatic fill, a direct POST).
    render(<ApiKeyCreateForm {...formProps} />);
    expect(nicknameInput().maxLength).toBe(MAX_KEY_NICKNAME_LEN);
  });

  it("an over-long nickname is refused client side without a request", async () => {
    const fetchMock = vi.fn();
    vi.stubGlobal("fetch", fetchMock);

    render(<ApiKeyCreateForm {...formProps} />);
    fireEvent.change(nicknameInput(), {
      target: { value: "A".repeat(MAX_KEY_NICKNAME_LEN + 1) },
    });
    fireEvent.click(submitButton());

    const alert = await screen.findByRole("alert");
    expect(alert.textContent).toContain(String(MAX_KEY_NICKNAME_LEN));
    expect(fetchMock).not.toHaveBeenCalled();
  });

  it("a nickname exactly at the cap is sent", async () => {
    const atCap = "A".repeat(MAX_KEY_NICKNAME_LEN);
    const fetchMock = vi.fn().mockResolvedValue(
      new Response(JSON.stringify(createdKeyBody()), { status: 201 }),
    );
    vi.stubGlobal("fetch", fetchMock);

    render(<ApiKeyCreateForm {...formProps} />);
    fireEvent.change(nicknameInput(), { target: { value: atCap } });
    fireEvent.click(submitButton());

    await screen.findByTestId("created-api-key-secret");
    expect(JSON.parse(String(fetchMock.mock.calls[0][1]?.body))).toEqual({
      nickname: atCap,
    });
  });

  it("an expiry already in the past is refused client side", async () => {
    const fetchMock = vi.fn();
    vi.stubGlobal("fetch", fetchMock);

    render(<ApiKeyCreateForm {...formProps} />);
    fireEvent.change(nicknameInput(), { target: { value: "dead-on-arrival" } });
    const expires = screen.getByLabelText(/expires/i);
    fireEvent.change(expires, { target: { value: "2020-01-01" } });
    fireEvent.click(submitButton());

    const alert = await screen.findByRole("alert");
    expect(alert.textContent).toContain("future");
    expect(fetchMock).not.toHaveBeenCalled();
  });

  it("create fires once with the trimmed nickname and no expiry field when unset", async () => {
    const fetchMock = vi.fn().mockResolvedValue(
      new Response(JSON.stringify(createdKeyBody()), { status: 201 }),
    );
    vi.stubGlobal("fetch", fetchMock);

    render(<ApiKeyCreateForm {...formProps} />);
    fireEvent.change(nicknameInput(), { target: { value: "  prod-server  " } });
    fireEvent.click(submitButton());

    await screen.findByTestId("created-api-key-secret");
    expect(fetchMock).toHaveBeenCalledTimes(1);
    const [url, init] = fetchMock.mock.calls[0];
    expect(String(url)).toBe(CREATE_URL);
    expect(init?.method).toBe("POST");
    expect(JSON.parse(String(init?.body))).toEqual({ nickname: "prod-server" });
    expect(screen.getByTestId("created-api-key-secret").textContent).toBe(
      "placeholder-not-a-real-key",
    );
    expect(refresh).toHaveBeenCalledTimes(1);
  });

  it("an expiry date is sent as an ISO timestamp", async () => {
    const fetchMock = vi.fn().mockResolvedValue(
      new Response(JSON.stringify(createdKeyBody()), { status: 201 }),
    );
    vi.stubGlobal("fetch", fetchMock);

    render(<ApiKeyCreateForm {...formProps} />);
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

    render(<ApiKeyCreateForm {...formProps} />);
    fireEvent.change(nicknameInput(), { target: { value: "prod" } });
    fireEvent.click(submitButton());

    const alert = await screen.findByRole("alert");
    expect(alert.textContent).toContain("Failed to create key");
    expect(screen.queryByTestId("created-api-key-secret")).toBeNull();
  });

  // Issue #1330. A workspace with no billing link used to receive a key, a
  // copy-it-now panel, and a 403 from the gateway on first use. It now receives
  // a refusal, and the whole point of that refusal is the sentence, so the
  // generic retry text must not overwrite it.
  it("an unprovisioned workspace sees the reason, not the generic retry text", async () => {
    const reason =
      "This workspace is not connected to billing, so a key created here would be rejected by the API.";
    const fetchMock = vi.fn().mockResolvedValue(
      new Response(
        JSON.stringify({ error: reason, code: "account_not_provisioned" }),
        { status: 409, headers: { "Content-Type": "application/json" } },
      ),
    );
    vi.stubGlobal("fetch", fetchMock);

    render(<ApiKeyCreateForm {...formProps} />);
    fireEvent.change(nicknameInput(), { target: { value: "prod" } });
    fireEvent.click(submitButton());

    const alert = await screen.findByRole("alert");
    expect(alert.textContent).toBe(reason);
    expect(screen.queryByTestId("created-api-key-secret")).toBeNull();
  });

  it("a refusal with no readable body still says something", async () => {
    const fetchMock = vi.fn().mockResolvedValue(
      new Response("<html>gateway</html>", { status: 409 }),
    );
    vi.stubGlobal("fetch", fetchMock);

    render(<ApiKeyCreateForm {...formProps} />);
    fireEvent.change(nicknameInput(), { target: { value: "prod" } });
    fireEvent.click(submitButton());

    const alert = await screen.findByRole("alert");
    expect(alert.textContent).toContain("Failed to create key");
  });

  // The proxy route answers an unrecognised refusal with a bare status class.
  // "Conflict" is developer jargon and is not what a customer should be shown,
  // so the absence of a machine code is what keeps the generic wording.
  it("an uncoded 409 keeps the generic wording rather than showing a status word", async () => {
    const fetchMock = vi.fn().mockResolvedValue(
      new Response(JSON.stringify({ error: "Conflict" }), {
        status: 409,
        headers: { "Content-Type": "application/json" },
      }),
    );
    vi.stubGlobal("fetch", fetchMock);

    render(<ApiKeyCreateForm {...formProps} />);
    fireEvent.change(nicknameInput(), { target: { value: "prod" } });
    fireEvent.click(submitButton());

    const alert = await screen.findByRole("alert");
    expect(alert.textContent).toContain("Failed to create key");
    expect(alert.textContent).not.toContain("Conflict");
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

    render(<ApiKeyCreateForm {...formProps} />);
    fireEvent.change(nicknameInput(), { target: { value: "prod" } });
    fireEvent.click(submitButton());
    await screen.findByTestId("created-api-key-secret");

    fireEvent.click(screen.getByRole("button", { name: /^Copy$/i }));
    await waitFor(() => {
      expect(writeText).toHaveBeenCalledWith("placeholder-not-a-real-key");
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

    render(<ApiKeyCreateForm {...formProps} />);
    fireEvent.change(nicknameInput(), { target: { value: "prod" } });
    fireEvent.click(submitButton());
    await screen.findByTestId("created-api-key-secret");

    fireEvent.click(screen.getByRole("button", { name: /^Copy$/i }));
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
    render(<ApiKeyCreateForm {...formProps} />);
    expect(cadenceField().hasAttribute("disabled")).toBe(false);
  });

  it("changing the cadence restates what the amount field means", () => {
    render(<ApiKeyCreateForm {...formProps} />);
    expect(screen.queryByText(/per calendar month/i)).toBeNull();
    fireEvent.change(cadenceField(), { target: { value: "monthly" } });
    expect(screen.getByText(/per calendar month/i)).toBeTruthy();
    expect(screen.queryByText(/for the key's lifetime/i)).toBeNull();
  });

  it("states the bound that will be enforced, not the field's name", () => {
    render(<ApiKeyCreateForm {...formProps} />);
    expect(summary()).toContain("No credit limit");
    fireEvent.change(limitField(), { target: { value: "10,000,000,000" } });
    expect(summary()).toContain("10,000,000,000 credits");
    expect(summary()).not.toMatch(/[$৳€£¥]|USD|BDT/);
    expect(summary()).toContain("spent in total");
    fireEvent.change(cadenceField(), { target: { value: "monthly" } });
    expect(summary()).toContain("current calendar month");
    fireEvent.change(limitField(), { target: { value: "0" } });
    expect(summary()).toContain("no limit will be applied");
  });

  it("warns on a cap small enough to be a figure typed in the old unit", () => {
    // The field kept its name and its syntax and changed its unit, so the
    // customer most at risk is the one who typed "10" before this shipped and
    // types "10" again now. Ten credits is refused on the first request, and
    // the figure itself reads as reasonable, so the sentence has to say so.
    render(<ApiKeyCreateForm {...formProps} />);
    fireEvent.change(limitField(), { target: { value: "10" } });
    expect(summary()).toContain("10 credits");
    expect(summary()).toContain("very small cap");
    // And the warning stays off a cap that is a real one, or it becomes noise
    // every customer learns to scroll past.
    fireEvent.change(limitField(), { target: { value: "10,000,000,000" } });
    expect(summary()).not.toContain("very small cap");
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
    render(<ApiKeyCreateForm {...formProps} />);
    fireEvent.change(nicknameInput(), { target: { value: "uncapped" } });
    fireEvent.click(submitButton());
    await screen.findByTestId("created-api-key-secret");
    expect(fetchMock).toHaveBeenCalledTimes(1);
    expect(limitCell()).toBe("Unlimited");
  });

  it("a lifetime cap reaches the wire as the exact integer the amount means", async () => {
    const fetchMock = okFetch();
    vi.stubGlobal("fetch", fetchMock);
    render(<ApiKeyCreateForm {...formProps} />);
    fireEvent.change(nicknameInput(), { target: { value: "capped" } });
    fireEvent.change(limitField(), { target: { value: "12,340,000,000" } });
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
    expect(limitCell()).toContain("12,340,000,000 credits");
    expect(limitCell()).not.toMatch(/[$৳€£¥]|USD|BDT/);
    expect(limitCell()).toContain("never resets");
  });

  it("the monthly cadence changes the budget kind on the wire", async () => {
    const fetchMock = okFetch();
    vi.stubGlobal("fetch", fetchMock);
    render(<ApiKeyCreateForm {...formProps} />);
    fireEvent.change(nicknameInput(), { target: { value: "monthly-capped" } });
    fireEvent.change(limitField(), { target: { value: "10000000000" } });
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
    render(<ApiKeyCreateForm {...formProps} />);
    fireEvent.change(nicknameInput(), { target: { value: "bad-limit" } });
    fireEvent.change(limitField(), { target: { value: "-5" } });
    fireEvent.click(submitButton());
    const alert = await screen.findByRole("alert");
    expect(alert.textContent).toContain("positive number of credits");
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
    render(<ApiKeyCreateForm {...formProps} />);
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
    render(<ApiKeyCreateForm {...formProps} />);
    fireEvent.change(nicknameInput(), { target: { value: "net-fail" } });
    fireEvent.change(limitField(), { target: { value: "5" } });
    fireEvent.click(submitButton());
    const secret = await screen.findByTestId("created-api-key-secret");
    expect(secret.textContent).toBe("placeholder-not-a-real-key");
    const alert = await screen.findByRole("alert");
    expect(alert.textContent).toContain("live and uncapped");
    expect(limitCell()).toBe("Unlimited");
  });
});

/**
 * Issue #550: the panel showed a secret and then stopped. It named no host, no
 * path and no way to test the key, so "mint a key, make a request" left the
 * product entirely. These tests pin the three things that fix has to hold: the
 * base URL is the one this deployment was configured with, the command is
 * runnable as rendered, and a response with no secret in it produces no command
 * at all rather than one that only looks runnable.
 */
describe("ApiKeyCreateForm quickstart", () => {
  async function mint(body: Record<string, unknown> = {}) {
    const fetchMock = vi
      .fn()
      .mockResolvedValue(
        new Response(JSON.stringify(createdKeyBody(body)), { status: 201 }),
      );
    vi.stubGlobal("fetch", fetchMock);
    render(<ApiKeyCreateForm {...formProps} />);
    fireEvent.change(nicknameInput(), { target: { value: "quickstart" } });
    fireEvent.click(submitButton());
    await screen.findByTestId("created-api-key-base-url");
  }

  it("states the base URL this deployment was configured with", async () => {
    await mint();

    expect(screen.getByTestId("created-api-key-base-url").textContent).toBe(
      "https://ai.acme-bank.internal/v1",
    );
  });

  it("renders a curl that runs verbatim, with the key just minted in it", async () => {
    await mint();

    const curl = screen.getByTestId("created-api-key-curl").textContent ?? "";
    expect(curl).toContain("https://ai.acme-bank.internal/v1/chat/completions");
    expect(curl).toContain('"model": "hive-chat-default"');
    expect(curl).toContain("Authorization: Bearer placeholder-not-a-real-key");
    // A placeholder here would be the whole defect: a command that looks
    // runnable, is not, and is the developer's first impression of the product.
    expect(curl).not.toContain("$HIVE_API_KEY");
  });

  it("copies the whole command, not just the key", async () => {
    const writeText = vi.fn().mockResolvedValue(undefined);
    Object.defineProperty(navigator, "clipboard", {
      value: { writeText },
      configurable: true,
    });
    await mint();

    fireEvent.click(screen.getByRole("button", { name: /copy command/i }));
    await waitFor(() => expect(writeText).toHaveBeenCalledTimes(1));
    expect(String(writeText.mock.calls[0][0])).toContain(
      "https://ai.acme-bank.internal/v1/chat/completions",
    );
  });

  it("renders no command at all when the response carried no secret", async () => {
    await mint({ secret: undefined });

    expect(screen.queryByTestId("created-api-key-curl")).toBeNull();
    // The base URL is still worth stating: it is true regardless of whether
    // this particular response carried a secret.
    expect(screen.getByTestId("created-api-key-base-url").textContent).toBe(
      "https://ai.acme-bank.internal/v1",
    );
    expect(
      screen.getByTestId("created-api-key-quickstart-note").textContent,
    ).toContain("secret was not returned");
  });
});
