// @vitest-environment jsdom

import "@testing-library/jest-dom/vitest";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

import { UserSettingsPanel } from "../src/features/settings/user-settings-panel";

const defaultSettings = {
  apiEnabled: true,
  generateImage: true,
  chatEnabled: true,
  billingEnabled: true,
  twoFactorEnabled: false,
};

describe("UserSettingsPanel", () => {
  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("loads settings and patches a toggled value", async () => {
    const fetchMock = vi
      .fn()
      .mockResolvedValueOnce({
        ok: true,
        json: async () => ({ settings: defaultSettings }),
      })
      .mockResolvedValueOnce({
        ok: true,
        json: async () => ({ settings: { ...defaultSettings, twoFactorEnabled: true } }),
      });

    vi.stubGlobal("fetch", fetchMock);

    render(<UserSettingsPanel apiKey="sk_test_123" />);

    expect(await screen.findByRole("switch", { name: /api enabled/i })).toBeInTheDocument();
    fireEvent.click(screen.getByRole("switch", { name: /two-factor enabled/i }));

    await waitFor(() => {
      expect(fetchMock).toHaveBeenCalledTimes(2);
      expect(fetchMock).toHaveBeenNthCalledWith(
        2,
        "http://127.0.0.1:8080/v1/users/settings",
        expect.objectContaining({
          method: "PATCH",
          body: JSON.stringify({ twoFactorEnabled: true }),
        }),
      );
    });
  });

  it("shows endpoint unavailable message on 404", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue({
        ok: false,
        status: 404,
        json: async () => ({ error: "Not found" }),
      }),
    );

    render(<UserSettingsPanel apiKey="sk_test_123" />);

    expect(await screen.findByText(/settings endpoint is not available yet/i)).toBeInTheDocument();
  });
});
