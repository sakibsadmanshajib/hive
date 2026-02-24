// @vitest-environment jsdom

import "@testing-library/jest-dom/vitest";
import { render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

const pushMock = vi.fn();

vi.mock("next/navigation", () => ({
  useRouter: () => ({
    push: pushMock,
  }),
}));

import ChatPage from "../src/app/chat/page";

describe("chat auth gate", () => {
  beforeEach(() => {
    window.localStorage.clear();
    pushMock.mockReset();
  });

  it("redirects unauthenticated users to auth page", async () => {
    render(<ChatPage />);

    await waitFor(() => {
      expect(pushMock).toHaveBeenCalledWith("/auth");
    });
    expect(screen.queryByText("Session setup")).not.toBeInTheDocument();
  });
});
