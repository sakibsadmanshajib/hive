import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { POST } from "../src/app/api/guest-session/link/route";
import { createGuestSession } from "../src/app/api/guest-session/session";

const originalInternalApiBaseUrl = process.env.INTERNAL_API_BASE_URL;
const originalPublicApiBaseUrl = process.env.NEXT_PUBLIC_API_BASE_URL;
const originalGuestToken = process.env.WEB_INTERNAL_GUEST_TOKEN;

describe("guest session link route", () => {
  beforeEach(() => {
    process.env.NEXT_PUBLIC_API_BASE_URL = "http://127.0.0.1:8080";
    process.env.INTERNAL_API_BASE_URL = "http://api:8080";
    process.env.WEB_INTERNAL_GUEST_TOKEN = "test-web-token";
    vi.restoreAllMocks();
  });

  afterEach(() => {
    if (originalGuestToken === undefined) {
      delete process.env.WEB_INTERNAL_GUEST_TOKEN;
    } else {
      process.env.WEB_INTERNAL_GUEST_TOKEN = originalGuestToken;
    }
    if (originalPublicApiBaseUrl === undefined) {
      delete process.env.NEXT_PUBLIC_API_BASE_URL;
    } else {
      process.env.NEXT_PUBLIC_API_BASE_URL = originalPublicApiBaseUrl;
    }
    if (originalInternalApiBaseUrl === undefined) {
      delete process.env.INTERNAL_API_BASE_URL;
    } else {
      process.env.INTERNAL_API_BASE_URL = originalInternalApiBaseUrl;
    }
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
    vi.resetModules();
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

  it("rejects linking without an authorization header", async () => {
    const { cookieValue } = createGuestSession("test-web-token", new Date("2026-03-13T00:00:00.000Z"));
    const fetchMock = vi.fn();
    vi.stubGlobal("fetch", fetchMock);

    const response = await POST(
      new Request("http://localhost/api/guest-session/link", {
        method: "POST",
        headers: {
          origin: "http://localhost",
          referer: "http://localhost/",
          cookie: `hive_guest_session=${cookieValue}`,
        },
      }),
    );

    expect(response.status).toBe(401);
    await expect(response.json()).resolves.toEqual({ error: "missing authorization" });
    expect(fetchMock).not.toHaveBeenCalled();
  });

  it("rejects linking without a guest session cookie", async () => {
    const fetchMock = vi.fn();
    vi.stubGlobal("fetch", fetchMock);

    const response = await POST(
      new Request("http://localhost/api/guest-session/link", {
        method: "POST",
        headers: {
          authorization: "Bearer access-token",
          origin: "http://localhost",
          referer: "http://localhost/",
        },
      }),
    );

    expect(response.status).toBe(401);
    await expect(response.json()).resolves.toEqual({ error: "missing guest session" });
    expect(fetchMock).not.toHaveBeenCalled();
  });

  it("rejects non-same-origin link attempts", async () => {
    const { cookieValue } = createGuestSession("test-web-token", new Date("2026-03-13T00:00:00.000Z"));
    const fetchMock = vi.fn();
    vi.stubGlobal("fetch", fetchMock);

    const response = await POST(
      new Request("http://localhost/api/guest-session/link", {
        method: "POST",
        headers: {
          authorization: "Bearer access-token",
          origin: "http://malicious.example",
          referer: "http://malicious.example/",
          cookie: `hive_guest_session=${cookieValue}`,
        },
      }),
    );

    expect(response.status).toBe(403);
    await expect(response.json()).resolves.toEqual({ error: "forbidden" });
    expect(fetchMock).not.toHaveBeenCalled();
  });

  it("returns 502 when the internal guest-link request fails", async () => {
    const { cookieValue } = createGuestSession("test-web-token", new Date("2026-03-13T00:00:00.000Z"));
    const fetchMock = vi.fn().mockRejectedValue(new Error("network down"));
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

    expect(response.status).toBe(502);
    await expect(response.json()).resolves.toEqual({ error: "guest session link unavailable" });
  });
});
