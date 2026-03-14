// @vitest-environment jsdom

import "@testing-library/jest-dom/vitest";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { clearAuthSession, writeAuthSession } from "../src/features/auth/auth-session";

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
  });

  it("renders chat workspace on root for authenticated users", async () => {
    writeAuthSession({ accessToken: "sk_test", email: "demo@example.com" });

    render(<HomePage />);

    expect((await screen.findAllByRole("button", { name: /new chat/i })).length).toBeGreaterThan(0);
  });

  it("tags authenticated chat requests as web traffic for reporting", async () => {
    process.env.NEXT_PUBLIC_API_BASE_URL = "http://127.0.0.1:8080";
    writeAuthSession({ accessToken: "sk_test", email: "demo@example.com" });
    const fetchMock = vi.fn()
      .mockResolvedValueOnce({
        ok: true,
        json: async () => ({
          data: [
            { id: "fast-chat", capability: "chat", costType: "fixed" },
          ],
        }),
      })
      .mockResolvedValueOnce({
        ok: true,
        json: async () => ({
          choices: [{ message: { content: "Authenticated reply" } }],
        }),
      });
    vi.stubGlobal("fetch", fetchMock);

    render(<HomePage />);

    const prompts = await screen.findAllByPlaceholderText(/ask something/i);
    fireEvent.change(prompts.at(-1)!, {
      target: { value: "hello from auth" },
    });
    const sendButtons = screen.getAllByRole("button", { name: /send/i });
    fireEvent.click(sendButtons.at(-1)!);

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
  });

  it("redirects legacy /chat route to root", async () => {
    render(<ChatPage />);

    await waitFor(() => {
      expect(replaceMock).toHaveBeenCalledWith("/");
    });
  });
});
