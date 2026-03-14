// @vitest-environment jsdom

import "@testing-library/jest-dom/vitest";
import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const { signInWithPasswordMock, signInWithOAuthMock, signUpMock } = vi.hoisted(() => ({
  signInWithPasswordMock: vi.fn(),
  signInWithOAuthMock: vi.fn(),
  signUpMock: vi.fn(),
}));

vi.mock("../src/lib/supabase-client", () => ({
  createSupabaseBrowserClient: () => ({
    auth: {
      signInWithPassword: signInWithPasswordMock,
      signInWithOAuth: signInWithOAuthMock,
      signUp: signUpMock,
    },
  }),
  useSupabaseAuthSessionSync: () => undefined,
}));

vi.mock("next/navigation", () => ({
  useRouter: () => ({
    push: vi.fn(),
    replace: vi.fn(),
  }),
}));

import HomePage from "../src/app/page";
import { clearAuthSession } from "../src/features/auth/auth-session";
import { clearGuestSession } from "../src/features/auth/guest-session";

function createGuestFetchMock() {
  return vi.fn(async (input: RequestInfo | URL) => {
    const url = typeof input === "string" ? input : input.toString();

    if (url.endsWith("/v1/models")) {
      return {
        ok: true,
        json: async () => ({
          data: [
            { id: "guest-free", capability: "chat", costType: "free" },
            { id: "fast-chat", capability: "chat", costType: "fixed" },
          ],
        }),
      };
    }

    if (url.endsWith("/api/guest-session")) {
      return {
        ok: true,
        json: async () => ({
          guestId: "guest_123",
          issuedAt: "2026-03-13T00:00:00.000Z",
          expiresAt: "2026-03-20T00:00:00.000Z",
        }),
      };
    }

    if (url.endsWith("/api/chat/guest")) {
      return {
        ok: true,
        json: async () => ({
          choices: [{ message: { content: "Guest reply" } }],
        }),
      };
    }

    return {
      ok: false,
      json: async () => ({ error: `Unhandled URL: ${url}` }),
    };
  });
}

describe("chat guest mode", () => {
  afterEach(() => {
    cleanup();
  });

  beforeEach(() => {
    window.localStorage.clear();
    clearAuthSession();
    clearGuestSession();
    vi.restoreAllMocks();
    signInWithPasswordMock.mockReset();
    signInWithOAuthMock.mockReset();
    signUpMock.mockReset();
    if (!HTMLElement.prototype.hasPointerCapture) {
      Object.defineProperty(HTMLElement.prototype, "hasPointerCapture", {
        configurable: true,
        value: () => false,
      });
    }
    if (!HTMLElement.prototype.setPointerCapture) {
      Object.defineProperty(HTMLElement.prototype, "setPointerCapture", {
        configurable: true,
        value: () => undefined,
      });
    }
    if (!HTMLElement.prototype.releasePointerCapture) {
      Object.defineProperty(HTMLElement.prototype, "releasePointerCapture", {
        configurable: true,
        value: () => undefined,
      });
    }
    if (!HTMLElement.prototype.scrollIntoView) {
      Object.defineProperty(HTMLElement.prototype, "scrollIntoView", {
        configurable: true,
        value: () => undefined,
      });
    }
  });

  async function openModelPicker() {
    const modelTrigger = (await screen.findAllByRole("combobox")).at(-1)!;
    fireEvent.keyDown(modelTrigger, { key: "ArrowDown" });
    return modelTrigger;
  }

  it("shows paid models as locked to guests instead of hiding them", async () => {
    const fetchMock = createGuestFetchMock();

    vi.stubGlobal("fetch", fetchMock);

    render(<HomePage />);

    await openModelPicker();

    expect(await screen.findByRole("option", { name: /fast-chat/i })).toBeInTheDocument();
    expect(screen.getByText(/locked/i)).toBeInTheDocument();
    expect(screen.getByText(/requires account and credits/i)).toBeInTheDocument();
  });

  it("opens a dismissible auth modal when a guest clicks a locked paid model", async () => {
    const fetchMock = createGuestFetchMock();

    vi.stubGlobal("fetch", fetchMock);

    render(<HomePage />);

    await openModelPicker();
    fireEvent.click(await screen.findByRole("option", { name: /fast-chat/i }));

    const dialog = await screen.findByRole("dialog");
    expect(within(dialog).getByRole("heading", { name: /login/i })).toBeInTheDocument();
    expect(within(dialog).getByRole("button", { name: /create account/i })).toBeInTheDocument();

    fireEvent.click(within(dialog).getByRole("button", { name: /continue with free models/i }));

    await waitFor(() => {
      expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    });
    expect((await screen.findAllByText(/guest mode is active/i)).length).toBeGreaterThan(0);
  });

  it("shows guest messaging and sends chat through the guest web endpoint", async () => {
    const fetchMock = createGuestFetchMock();

    vi.stubGlobal("fetch", fetchMock);

    render(<HomePage />);

    expect((await screen.findAllByText(/guest mode is active/i)).length).toBeGreaterThan(0);
    await waitFor(() => {
      expect(fetchMock.mock.calls.some(([url, init]) =>
        /\/api\/guest-session$/.test(String(url)) &&
        typeof init === "object" &&
        init !== null &&
        "method" in init &&
        init.method === "POST"
      )).toBe(true);
    });

    fireEvent.change(screen.getByPlaceholderText(/ask something/i), {
      target: { value: "hello from guest" },
    });
    fireEvent.click(screen.getByRole("button", { name: /send/i }));

    await waitFor(() => {
      expect(fetchMock.mock.calls.some(([url, init]) =>
        /\/api\/chat\/guest$/.test(String(url)) &&
        typeof init === "object" &&
        init !== null &&
        "method" in init &&
        init.method === "POST"
      )).toBe(true);
    });

    expect(screen.getByText(/guest mode only supports free models/i)).toBeInTheDocument();
  });

  it("falls back to the built-in guest-safe model when the catalog request fails in guest mode", async () => {
    const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
      const url = typeof input === "string" ? input : input.toString();

      if (url.endsWith("/v1/models")) {
        return {
          ok: false,
          json: async () => ({ error: "catalog unavailable" }),
        };
      }

      if (url.endsWith("/api/guest-session")) {
        return {
          ok: true,
          json: async () => ({
            guestId: "guest_123",
            issuedAt: "2026-03-13T00:00:00.000Z",
            expiresAt: "2026-03-20T00:00:00.000Z",
          }),
        };
      }

      return {
        ok: true,
        json: async () => ({
          choices: [{ message: { content: "Guest reply" } }],
        }),
      };
    });

    vi.stubGlobal("fetch", fetchMock);

    render(<HomePage />);

    const modelTrigger = (await screen.findAllByRole("combobox")).at(-1)!;
    await waitFor(() => {
      expect(modelTrigger).toHaveTextContent("guest-free");
    });
    fireEvent.keyDown(modelTrigger, { key: "ArrowDown" });

    expect(screen.queryByRole("option", { name: /fast-chat/i })).not.toBeInTheDocument();
  });

  it("keeps the built-in guest-safe model active when the catalog returns only paid chat models", async () => {
    const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = typeof input === "string" ? input : input.toString();

      if (url.endsWith("/v1/models")) {
        return {
          ok: true,
          json: async () => ({
            data: [
              { id: "fast-chat", capability: "chat", costType: "fixed" },
              { id: "smart-reasoning", capability: "chat", costType: "variable" },
            ],
          }),
        };
      }

      if (url.endsWith("/api/guest-session")) {
        return {
          ok: true,
          json: async () => ({
            guestId: "guest_123",
            issuedAt: "2026-03-13T00:00:00.000Z",
            expiresAt: "2026-03-20T00:00:00.000Z",
          }),
        };
      }

      if (url.endsWith("/api/chat/guest")) {
        return {
          ok: true,
          json: async () => ({
            choices: [{ message: { content: "Guest reply" } }],
          }),
        };
      }

      return {
        ok: false,
        json: async () => ({ error: `Unhandled URL: ${url}` }),
      };
    });

    vi.stubGlobal("fetch", fetchMock);

    render(<HomePage />);

    const modelTrigger = (await screen.findAllByRole("combobox")).at(-1)!;
    await waitFor(() => {
      expect(modelTrigger).toHaveTextContent("guest-free");
    });

    fireEvent.change(screen.getByPlaceholderText(/ask something/i), {
      target: { value: "hello from guest" },
    });
    fireEvent.click(screen.getByRole("button", { name: /send/i }));

    await waitFor(() => {
      expect(fetchMock.mock.calls.some(([url, init]) => {
        if (!/\/api\/chat\/guest$/.test(String(url))) {
          return false;
        }
        if (!init || typeof init !== "object" || !("body" in init)) {
          return false;
        }
        return JSON.parse(String(init.body)).model === "guest-free";
      })).toBe(true);
    });
  });

  it("unlocks paid models in place after authenticating from the modal", async () => {
    const fetchMock = createGuestFetchMock();
    signInWithPasswordMock.mockResolvedValue({
      data: {
        session: {
          access_token: "auth_token",
        },
        user: {
          email: "demo@example.com",
          user_metadata: { name: "Demo" },
        },
      },
      error: null,
    });

    vi.stubGlobal("fetch", fetchMock);

    render(<HomePage />);

    await openModelPicker();
    fireEvent.click(await screen.findByRole("option", { name: /fast-chat/i }));

    const dialog = await screen.findByRole("dialog");
    fireEvent.change(within(dialog).getAllByPlaceholderText("Email")[0]!, {
      target: { value: "demo@example.com" },
    });
    fireEvent.change(within(dialog).getAllByPlaceholderText("Password")[0]!, {
      target: { value: "password123" },
    });
    fireEvent.click(within(dialog).getAllByRole("button", { name: /login/i }).at(-1)!);

    await waitFor(() => {
      expect(signInWithPasswordMock).toHaveBeenCalledWith({
        email: "demo@example.com",
        password: "password123",
      });
    });
    await waitFor(() => {
      expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    });
    await waitFor(() => {
      expect(screen.queryByText(/guest mode is active/i)).not.toBeInTheDocument();
    });

    await openModelPicker();
    const paidOption = await screen.findByRole("option", { name: /fast-chat/i });
    expect(within(paidOption).queryByText(/locked/i)).not.toBeInTheDocument();
    fireEvent.click(paidOption);

    const modelTrigger = (await screen.findAllByRole("combobox")).at(-1)!;
    await waitFor(() => {
      expect(modelTrigger).toHaveTextContent("fast-chat");
    });
  });
});
