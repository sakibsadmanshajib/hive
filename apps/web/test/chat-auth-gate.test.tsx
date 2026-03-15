// @vitest-environment jsdom

import "@testing-library/jest-dom/vitest";
import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { clearAuthSession, replaceAuthSession, writeAuthSession } from "../src/features/auth/auth-session";
import { clearGuestSession } from "../src/features/auth/guest-session";

vi.mock("../src/lib/supabase-client", () => ({
  useSupabaseAuthSessionSync: () => undefined,
}));

const pushMock = vi.fn();
const replaceMock = vi.fn();
const originalPublicApiBaseUrl = process.env.NEXT_PUBLIC_API_BASE_URL;

vi.mock("next/navigation", () => ({
  useRouter: () => ({
    push: pushMock,
    replace: replaceMock,
  }),
}));

import HomePage from "../src/app/page";
import ChatPage from "../src/app/chat/page";

describe("chat auth gate", () => {
  beforeEach(() => {
    window.localStorage.clear();
    clearGuestSession();
    pushMock.mockReset();
    replaceMock.mockReset();
  });

  afterEach(() => {
    clearAuthSession();
    if (originalPublicApiBaseUrl === undefined) {
      delete process.env.NEXT_PUBLIC_API_BASE_URL;
    } else {
      process.env.NEXT_PUBLIC_API_BASE_URL = originalPublicApiBaseUrl;
    }
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
  });

  it("renders chat workspace on root for unauthenticated users", async () => {
    render(<HomePage />);

    expect((await screen.findAllByRole("button", { name: /new chat/i })).length).toBeGreaterThan(0);
    expect(pushMock).not.toHaveBeenCalled();
    expect(replaceMock).not.toHaveBeenCalled();
  });

  it("renders chat workspace on root for authenticated users", async () => {
    writeAuthSession({ accessToken: "sk_test", email: "demo@example.com" });

    render(<HomePage />);

    expect((await screen.findAllByRole("button", { name: /new chat/i })).length).toBeGreaterThan(0);
  });

  it("clears guest conversation state when the browser session becomes authenticated", async () => {
    process.env.NEXT_PUBLIC_API_BASE_URL = "http://127.0.0.1:8080";
    const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
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

      if (url === "http://127.0.0.1:8080/v1/chat/completions") {
        return {
          ok: true,
          json: async () => ({
            choices: [{ message: { content: "Authenticated reply" } }],
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

    const prompt = (await screen.findAllByPlaceholderText(/ask something/i)).at(-1)!;
    fireEvent.change(prompt, {
      target: { value: "guest hello" },
    });
    const composer = prompt.closest("div.space-y-3");
    if (!composer) {
      throw new Error("expected prompt to be inside a message composer");
    }
    fireEvent.click(within(composer).getByRole("button", { name: /send/i }));

    expect(await screen.findByText("guest hello")).toBeInTheDocument();
    expect(await screen.findByText("Guest reply")).toBeInTheDocument();

    writeAuthSession({ accessToken: "sk_test", email: "demo@example.com" });

    await waitFor(() => {
      expect(screen.queryByText(/guest mode is active/i)).not.toBeInTheDocument();
    });
    await waitFor(() => {
      expect(screen.queryByText("guest hello")).not.toBeInTheDocument();
      expect(screen.queryByText("Guest reply")).not.toBeInTheDocument();
    });
  });

  it("tags authenticated chat requests as web traffic for reporting", async () => {
    process.env.NEXT_PUBLIC_API_BASE_URL = "http://127.0.0.1:8080";
    writeAuthSession({ accessToken: "sk_test", email: "demo@example.com" });
    const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
      const url = typeof input === "string" ? input : input.toString();

      if (url === "http://127.0.0.1:8080/v1/chat/completions") {
        return {
          ok: true,
          json: async () => ({
            choices: [{ message: { content: "Authenticated reply" } }],
          }),
        };
      }

      return {
        ok: true,
        json: async () => ({
          data: [
            { id: "fast-chat", capability: "chat", costType: "fixed" },
          ],
        }),
      };
    });
    vi.stubGlobal("fetch", fetchMock);

    render(<HomePage />);

    const prompt = (await screen.findAllByPlaceholderText(/ask something/i)).at(-1)!;
    fireEvent.change(prompt, {
      target: { value: "hello from auth" },
    });
    const composer = prompt.closest("div.space-y-3");
    if (!composer) {
      throw new Error("expected prompt to be inside a message composer");
    }
    fireEvent.click(within(composer).getByRole("button", { name: /send/i }));

    await waitFor(() => {
      expect(fetchMock.mock.calls.some(([url, init]) => {
        if (url !== "http://127.0.0.1:8080/v1/chat/completions") {
          return false;
        }
        const headers = init && typeof init === "object" && "headers" in init
          ? init.headers as Record<string, string>
          : undefined;
        return headers?.Authorization === "Bearer sk_test" && headers["content-type"] === "application/json";
      })).toBe(true);
    });
    expect(fetchMock.mock.calls.some(([url]) => /\/api\/guest-session$/.test(String(url)))).toBe(false);
  });

  it("does not reset authenticated chat state when the access token refreshes for the same stored identity", async () => {
    process.env.NEXT_PUBLIC_API_BASE_URL = "http://127.0.0.1:8080";
    replaceAuthSession({ accessToken: "sk_old", email: "demo@example.com" });
    const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
      const url = typeof input === "string" ? input : input.toString();

      if (url.endsWith("/v1/models")) {
        return {
          ok: true,
          json: async () => ({
            data: [{ id: "fast-chat", capability: "chat", costType: "fixed" }],
          }),
        };
      }

      if (url === "http://127.0.0.1:8080/v1/chat/completions") {
        return {
          ok: true,
          json: async () => ({
            choices: [{ message: { content: "Authenticated reply" } }],
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

    const prompt = (await screen.findAllByPlaceholderText(/ask something/i)).at(-1)!;
    fireEvent.change(prompt, {
      target: { value: "hello from auth" },
    });
    const composer = prompt.closest("div.space-y-3");
    if (!composer) {
      throw new Error("expected prompt to be inside a message composer");
    }
    fireEvent.click(within(composer).getByRole("button", { name: /send/i }));

    expect((await screen.findAllByText("hello from auth")).length).toBeGreaterThan(0);
    expect((await screen.findAllByText("Authenticated reply")).length).toBeGreaterThan(0);

    replaceAuthSession({ accessToken: "sk_new", email: "demo@example.com" });

    await waitFor(() => {
      expect(screen.getAllByText("hello from auth").length).toBeGreaterThan(0);
      expect(screen.getAllByText("Authenticated reply").length).toBeGreaterThan(0);
    });
  });

  it("redirects legacy /chat route to root", async () => {
    render(<ChatPage />);

    await waitFor(() => {
      expect(replaceMock).toHaveBeenCalledWith("/");
    });
  });
});
