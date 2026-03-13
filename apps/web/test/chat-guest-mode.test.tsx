// @vitest-environment jsdom

import "@testing-library/jest-dom/vitest";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

vi.mock("../src/lib/supabase-client", () => ({
  useSupabaseAuthSessionSync: () => undefined,
}));

vi.mock("next/navigation", () => ({
  useRouter: () => ({
    push: vi.fn(),
    replace: vi.fn(),
  }),
}));

import HomePage from "../src/app/page";

describe("chat guest mode", () => {
  beforeEach(() => {
    window.localStorage.clear();
    vi.restoreAllMocks();
  });

  it("shows guest messaging and sends chat through the guest web endpoint", async () => {
    const fetchMock = vi.fn()
      .mockResolvedValueOnce({
        ok: true,
        json: async () => ({
          data: [
            { id: "guest-free", capability: "chat", costType: "free" },
            { id: "fast-chat", capability: "chat", costType: "fixed" },
          ],
        }),
      })
      .mockResolvedValueOnce({
        ok: true,
        json: async () => ({
          guestId: "guest_123",
          issuedAt: "2026-03-13T00:00:00.000Z",
          expiresAt: "2026-03-20T00:00:00.000Z",
        }),
      })
      .mockResolvedValueOnce({
        ok: true,
        json: async () => ({
          choices: [{ message: { content: "Guest reply" } }],
        }),
      });

    vi.stubGlobal("fetch", fetchMock);

    render(<HomePage />);

    expect(await screen.findByText(/guest mode is active/i)).toBeInTheDocument();
    await waitFor(() => {
      expect(fetchMock).toHaveBeenNthCalledWith(
        2,
        expect.stringMatching(/\/api\/guest-session$/),
        expect.objectContaining({
          method: "POST",
        }),
      );
    });

    fireEvent.change(screen.getByPlaceholderText(/ask something/i), {
      target: { value: "hello from guest" },
    });
    fireEvent.click(screen.getByRole("button", { name: /send/i }));

    await waitFor(() => {
      expect(fetchMock).toHaveBeenNthCalledWith(
        3,
        expect.stringMatching(/\/api\/chat\/guest$/),
        expect.objectContaining({
          method: "POST",
        }),
      );
    });

    expect(screen.getByText(/guest mode only supports free models/i)).toBeInTheDocument();
  });
});
