// @vitest-environment jsdom

import "@testing-library/jest-dom/vitest";
import { render, screen, waitFor } from "@testing-library/react";
import { createElement } from "react";
import { beforeEach, describe, expect, it } from "vitest";

import {
  clearGuestSession,
  GUEST_SESSION_STORAGE_KEY,
  readGuestSession,
  useGuestSession,
  writeGuestSession,
} from "../src/features/auth/guest-session";

function GuestSessionProbe() {
  const session = useGuestSession();
  return createElement("div", null, session?.guestId ?? "none");
}

describe("guest session", () => {
  beforeEach(() => {
    window.localStorage.clear();
    clearGuestSession();
  });

  it("persists and reads guest session payload", () => {
    writeGuestSession({
      guestId: "guest_123",
      issuedAt: "2026-03-13T00:00:00.000Z",
      expiresAt: "2026-03-20T00:00:00.000Z",
    });

    expect(window.localStorage.getItem(GUEST_SESSION_STORAGE_KEY)).toEqual(
      JSON.stringify({
        guestId: "guest_123",
        issuedAt: "2026-03-13T00:00:00.000Z",
        expiresAt: "2026-03-20T00:00:00.000Z",
      }),
    );
    expect(readGuestSession()).toEqual({
      guestId: "guest_123",
      issuedAt: "2026-03-13T00:00:00.000Z",
      expiresAt: "2026-03-20T00:00:00.000Z",
    });
  });

  it("updates subscribers when the guest session changes", async () => {
    render(createElement(GuestSessionProbe));

    expect(screen.getByText("none")).toBeInTheDocument();

    writeGuestSession({
      guestId: "guest_456",
      issuedAt: "2026-03-13T00:00:00.000Z",
      expiresAt: "2026-03-20T00:00:00.000Z",
    });

    await waitFor(() => {
      expect(screen.getByText("guest_456")).toBeInTheDocument();
    });

    clearGuestSession();

    await waitFor(() => {
      expect(screen.getByText("none")).toBeInTheDocument();
    });
  });
});
