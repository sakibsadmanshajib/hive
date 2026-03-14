import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { POST } from "../src/app/api/guest-session/route";

const originalInternalApiBaseUrl = process.env.INTERNAL_API_BASE_URL;
const originalPublicApiBaseUrl = process.env.NEXT_PUBLIC_API_BASE_URL;
const originalGuestToken = process.env.WEB_INTERNAL_GUEST_TOKEN;

describe("guest session bootstrap route", () => {
  beforeEach(() => {
    process.env.WEB_INTERNAL_GUEST_TOKEN = "test-web-token";
    process.env.NEXT_PUBLIC_API_BASE_URL = "http://127.0.0.1:8080";
    process.env.INTERNAL_API_BASE_URL = "http://api:8080";
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
      "http://api:8080/v1/internal/guest/session",
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

  it("accepts loopback alias origins used by the production smoke base URL", async () => {
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
          origin: "http://127.0.0.1",
          referer: "http://127.0.0.1/",
        },
      }),
    );

    expect(response.status).toBe(200);
    expect(fetchMock).toHaveBeenCalledTimes(1);
  });

  it("fails closed when the internal guest token is not configured", async () => {
    delete process.env.WEB_INTERNAL_GUEST_TOKEN;
    const fetchMock = vi.fn();
    vi.stubGlobal("fetch", fetchMock);

    const response = await POST(
      new Request("http://localhost/api/guest-session", {
        method: "POST",
        headers: {
          origin: "http://localhost",
          referer: "http://localhost/",
        },
      }),
    );

    expect(response.status).toBe(503);
    await expect(response.json()).resolves.toEqual({ error: "guest chat unavailable" });
    expect(fetchMock).not.toHaveBeenCalled();
  });

  it("rejects non-same-origin guest bootstrap requests", async () => {
    const fetchMock = vi.fn();
    vi.stubGlobal("fetch", fetchMock);

    const response = await POST(
      new Request("http://localhost/api/guest-session", {
        method: "POST",
        headers: {
          origin: "http://malicious.example",
          referer: "http://malicious.example/",
        },
      }),
    );

    expect(response.status).toBe(403);
    await expect(response.json()).resolves.toEqual({ error: "forbidden" });
    expect(fetchMock).not.toHaveBeenCalled();
  });
});
