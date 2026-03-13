import { beforeEach, describe, expect, it, vi } from "vitest";
import { POST } from "../src/app/api/chat/guest/route";
import { createGuestSession } from "../src/app/api/guest-session/session";

describe("guest chat web route", () => {
  beforeEach(() => {
    process.env.NEXT_PUBLIC_API_BASE_URL = "http://127.0.0.1:8080";
    process.env.WEB_INTERNAL_GUEST_TOKEN = "test-web-token";
    vi.restoreAllMocks();
  });

  it("forwards guest chat to the internal API with the trusted web token", async () => {
    const { cookieValue } = createGuestSession("test-web-token", new Date("2026-03-13T00:00:00.000Z"));
    const fetchMock = vi.fn().mockResolvedValue({
      ok: true,
      status: 200,
      json: async () => ({
        choices: [{ message: { content: "Guest reply" } }],
      }),
    });
    vi.stubGlobal("fetch", fetchMock);

    const response = await POST(
      new Request("http://localhost/api/chat/guest", {
        method: "POST",
        headers: {
          "content-type": "application/json",
          origin: "http://localhost",
          referer: "http://localhost/",
          "x-forwarded-for": "203.0.113.10, 10.0.0.2",
          cookie: `hive_guest_session=${cookieValue}`,
        },
        body: JSON.stringify({
          model: "guest-free",
          messages: [{ role: "user", content: "hello" }],
        }),
      }),
    );

    expect(fetchMock).toHaveBeenCalledWith(
      "http://127.0.0.1:8080/v1/internal/chat/guest",
      expect.objectContaining({
        method: "POST",
        headers: expect.objectContaining({
          "content-type": "application/json",
          "x-web-guest-token": "test-web-token",
          "x-guest-client-ip": "203.0.113.10",
        }),
      }),
    );
    expect(response.status).toBe(200);
    await expect(response.json()).resolves.toEqual({
      choices: [{ message: { content: "Guest reply" } }],
    });
  });

  it("rejects guest chat when the guest session cookie is missing", async () => {
    const fetchMock = vi.fn();
    vi.stubGlobal("fetch", fetchMock);

    const response = await POST(
      new Request("http://localhost/api/chat/guest", {
        method: "POST",
        headers: {
          "content-type": "application/json",
          origin: "http://localhost",
          referer: "http://localhost/",
        },
        body: JSON.stringify({
          model: "guest-free",
          messages: [{ role: "user", content: "hello" }],
        }),
      }),
    );

    expect(fetchMock).not.toHaveBeenCalled();
    expect(response.status).toBe(401);
    await expect(response.json()).resolves.toEqual({ error: "missing guest session" });
  });

  it("rejects guest chat when the request is not same-origin browser traffic", async () => {
    const fetchMock = vi.fn();
    vi.stubGlobal("fetch", fetchMock);

    const response = await POST(
      new Request("http://localhost/api/chat/guest", {
        method: "POST",
        headers: {
          "content-type": "application/json",
          origin: "http://evil.example",
        },
        body: JSON.stringify({
          model: "guest-free",
          messages: [{ role: "user", content: "hello" }],
        }),
      }),
    );

    expect(fetchMock).not.toHaveBeenCalled();
    expect(response.status).toBe(403);
    await expect(response.json()).resolves.toEqual({ error: "forbidden" });
  });
});
