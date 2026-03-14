import { beforeEach, describe, expect, it, vi } from "vitest";
import { POST } from "../src/app/api/guest-session/link/route";
import { createGuestSession } from "../src/app/api/guest-session/session";

describe("guest session link route", () => {
  beforeEach(() => {
    process.env.NEXT_PUBLIC_API_BASE_URL = "http://127.0.0.1:8080";
    process.env.INTERNAL_API_BASE_URL = "http://api:8080";
    process.env.WEB_INTERNAL_GUEST_TOKEN = "test-web-token";
    vi.restoreAllMocks();
  });

  it("forwards trusted guest linking to the internal API", async () => {
    const { cookieValue } = createGuestSession("test-web-token", new Date("2026-03-13T00:00:00.000Z"));
    const fetchMock = vi.fn().mockResolvedValue({
      ok: true,
      status: 200,
      json: async () => ({ linked: true }),
    });
    vi.stubGlobal("fetch", fetchMock);

    const response = await POST(
      new Request("http://localhost/api/guest-session/link", {
        method: "POST",
        headers: {
          authorization: "Bearer access-token",
          origin: "http://localhost",
          referer: "http://localhost/",
          cookie: `hive_guest_session=${cookieValue}`,
        },
      }),
    );

    expect(fetchMock).toHaveBeenCalledWith(
      "http://api:8080/v1/internal/guest/link",
      expect.objectContaining({
        method: "POST",
        headers: expect.objectContaining({
          authorization: "Bearer access-token",
          "x-web-guest-token": "test-web-token",
        }),
      }),
    );
    expect(response.status).toBe(200);
    await expect(response.json()).resolves.toEqual({ linked: true });
  });
});
