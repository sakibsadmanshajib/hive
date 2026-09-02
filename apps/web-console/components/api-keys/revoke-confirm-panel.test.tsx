/**
 * Behavioral tests for RevokeConfirmPanel: the two step confirm before an
 * API key is permanently revoked. Revocation is irreversible, so the suite
 * pins both the cancel path (no request) and the confirm path (exactly one
 * revoke POST for the right key id).
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

import { RevokeConfirmPanel } from "./revoke-confirm-panel";

function revokeUrl(keyId: string): string {
  return `/api/v1/accounts/current/api-keys/${keyId}/revoke`;
}

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
  refresh.mockClear();
});

describe("RevokeConfirmPanel", () => {
  it("starts as a Revoke button with no confirmation surface", () => {
    render(
      <RevokeConfirmPanel keyId="key-42" keyNickname="prod-server" />,
    );
    expect(screen.getByRole("button", { name: "Revoke" })).toBeTruthy();
    expect(screen.queryByText(/revoke this key/i)).toBeNull();
  });

  it("clicking Revoke shows the confirm panel naming the key", () => {
    render(
      <RevokeConfirmPanel keyId="key-42" keyNickname="prod-server" />,
    );
    fireEvent.click(screen.getByRole("button", { name: "Revoke" }));
    expect(screen.getByText(/revoke this key\?/i)).toBeTruthy();
    expect(screen.getByText(/prod-server/)).toBeTruthy();
    expect(screen.getByRole("button", { name: /keep key/i })).toBeTruthy();
  });

  it("Keep key cancels without any network call", async () => {
    const fetchMock = vi.fn();
    vi.stubGlobal("fetch", fetchMock);
    const onCancel = vi.fn();

    render(
      <RevokeConfirmPanel
        keyId="key-42"
        keyNickname="prod-server"
        onCancel={onCancel}
      />,
    );
    fireEvent.click(screen.getByRole("button", { name: "Revoke" }));
    fireEvent.click(screen.getByRole("button", { name: /keep key/i }));

    expect(onCancel).toHaveBeenCalledTimes(1);
    expect(fetchMock).not.toHaveBeenCalled();
    // Back to the idle trigger.
    expect(screen.getByRole("button", { name: "Revoke" })).toBeTruthy();
  });

  it("confirming fires exactly one revoke POST for the right key id and completes", async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response("{}", { status: 200 }));
    vi.stubGlobal("fetch", fetchMock);
    const onComplete = vi.fn();

    render(
      <RevokeConfirmPanel
        keyId="key-42"
        keyNickname="prod-server"
        onComplete={onComplete}
      />,
    );
    fireEvent.click(screen.getByRole("button", { name: "Revoke" }));
    fireEvent.click(screen.getByRole("button", { name: /revoke key/i }));

    await waitFor(() => {
      expect(fetchMock).toHaveBeenCalledTimes(1);
    });
    const [url, init] = fetchMock.mock.calls[0];
    expect(String(url)).toBe(revokeUrl("key-42"));
    expect(init?.method).toBe("POST");
    expect(onComplete).toHaveBeenCalledTimes(1);
    // Panel collapses back to the idle trigger after success.
    expect(screen.getByRole("button", { name: "Revoke" })).toBeTruthy();
  });

  it("failed revoke surfaces an error and keeps the panel open", async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response("locked", { status: 409 }));
    vi.stubGlobal("fetch", fetchMock);

    render(
      <RevokeConfirmPanel keyId="key-42" keyNickname="prod-server" />,
    );
    fireEvent.click(screen.getByRole("button", { name: "Revoke" }));
    fireEvent.click(screen.getByRole("button", { name: /revoke key/i }));

    const alert = await screen.findByRole("alert");
    expect(alert.textContent).toContain("Failed to revoke key");
    expect(screen.queryByRole("button", { name: /revoke key/i })).toBeTruthy();
  });

  it("network failure during revoke also keeps the panel open", async () => {
    const fetchMock = vi.fn().mockRejectedValue(new TypeError("offline"));
    vi.stubGlobal("fetch", fetchMock);

    render(
      <RevokeConfirmPanel keyId="key-42" keyNickname="prod-server" />,
    );
    fireEvent.click(screen.getByRole("button", { name: "Revoke" }));
    fireEvent.click(screen.getByRole("button", { name: /revoke key/i }));

    const alert = await screen.findByRole("alert");
    expect(alert.textContent).toContain("offline");
    expect(screen.queryByRole("button", { name: /revoke key/i })).toBeTruthy();
  });
});
