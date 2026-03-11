// @vitest-environment jsdom

import "@testing-library/jest-dom/vitest";
import { render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { writeAuthSession } from "../src/features/auth/auth-session";

vi.mock("../src/lib/supabase-client", () => ({
  useSupabaseAuthSessionSync: () => undefined,
}));

const pushMock = vi.fn();
const replaceMock = vi.fn();

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

  it("redirects unauthenticated users to auth page", async () => {
    render(<HomePage />);

    await waitFor(() => {
      expect(pushMock).toHaveBeenCalledWith("/auth");
    });
    expect(screen.queryByRole("button", { name: /new chat/i })).not.toBeInTheDocument();
  });

  it("renders chat workspace on root for authenticated users", async () => {
    writeAuthSession({ accessToken: "sk_test", email: "demo@example.com" });

    render(<HomePage />);

    expect((await screen.findAllByRole("button", { name: /new chat/i })).length).toBeGreaterThan(0);
  });

  it("redirects legacy /chat route to root", async () => {
    render(<ChatPage />);

    await waitFor(() => {
      expect(replaceMock).toHaveBeenCalledWith("/");
    });
  });
});
