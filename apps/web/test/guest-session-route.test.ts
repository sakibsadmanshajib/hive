import { beforeEach, describe, expect, it, vi } from "vitest";
import { POST } from "../src/app/api/guest-session/route";

describe("guest session bootstrap route", () => {
  beforeEach(() => {
    process.env.WEB_INTERNAL_GUEST_TOKEN = "test-web-token";
    process.env.NEXT_PUBLIC_API_BASE_URL = "http://127.0.0.1:8080";
  });

  it("issues a guest session cookie and returns a browser-visible guest session object", async () => {
    const fetchMock = vi.fn().mockResolvedValue({
      ok: true,
      status: 201,
      json: async () => ({ persisted: true }),
    });
    vi.stubGlobal("fetch", fetchMock);

    const response = await POST(
      new Request("http://localhost/api/guest-session", {
        method: "POST",
        headers: {
          origin: "http://localhost",
          referer: "http://localhost/",
          "x-forwarded-for": "203.0.113.10, 10.0.0.1",
        },
      }),
    );

    expect(response.status).toBe(200);
    expect(fetchMock).toHaveBeenCalledWith(
      "http://127.0.0.1:8080/v1/internal/guest/session",
      expect.objectContaining({
        method: "POST",
        headers: expect.objectContaining({
          "x-web-guest-token": "test-web-token",
        }),
      }),
    );
    expect(response.headers.get("set-cookie")).toContain("hive_guest_session=");
    await expect(response.json()).resolves.toMatchObject({
      guestId: expect.stringMatching(/^guest_/),
      issuedAt: expect.any(String),
      expiresAt: expect.any(String),
    });
  });
});
