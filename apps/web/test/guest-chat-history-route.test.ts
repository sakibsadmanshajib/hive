import { beforeEach, describe, expect, it, vi } from "vitest";
import { GET as getSessions, POST as postSession } from "../src/app/api/chat/guest/sessions/route";
import { GET as getSessionById } from "../src/app/api/chat/guest/sessions/[sessionId]/route";
import { POST as postMessage } from "../src/app/api/chat/guest/sessions/[sessionId]/messages/route";
import { createGuestSession } from "../src/app/api/guest-session/session";

describe("guest chat history web routes", () => {
  beforeEach(() => {
    process.env.NEXT_PUBLIC_API_BASE_URL = "http://127.0.0.1:8080";
    process.env.INTERNAL_API_BASE_URL = "http://api:8080";
    process.env.WEB_INTERNAL_GUEST_TOKEN = "test-web-token";
    vi.restoreAllMocks();
  });

  function requestOptions(cookieValue: string, overrides: RequestInit = {}) {
    return {
      headers: {
        origin: "http://localhost",
        referer: "http://localhost/",
        cookie: `hive_guest_session=${cookieValue}`,
        ...overrides.headers,
      },
      ...overrides,
    };
  }

  it("GET /api/chat/guest/sessions proxies list to internal API with guest token", async () => {
    const { cookieValue } = createGuestSession("test-web-token", new Date("2026-03-15T00:00:00.000Z"));
    const fetchMock = vi.fn().mockResolvedValue({
      ok: true,
      status: 200,
      headers: new Headers({ "content-type": "application/json" }),
      text: async () => JSON.stringify({ object: "list", data: [] }),
    });
    vi.stubGlobal("fetch", fetchMock);

    const response = await getSessions(
      new Request("http://localhost/api/chat/guest/sessions", {
        method: "GET",
        ...requestOptions(cookieValue),
      }),
    );

    expect(fetchMock).toHaveBeenCalledWith(
      "http://api:8080/v1/internal/guest/chat/sessions",
      expect.objectContaining({
        method: "GET",
        headers: expect.objectContaining({
          "x-web-guest-token": "test-web-token",
          "x-guest-id": expect.stringMatching(/^guest_[a-f0-9]+$/),
        }),
      }),
    );
    expect(response.status).toBe(200);
    await expect(response.json()).resolves.toEqual({ object: "list", data: [] });
  });

  it("POST /api/chat/guest/sessions proxies create to internal API", async () => {
    const { cookieValue } = createGuestSession("test-web-token");
    const fetchMock = vi.fn().mockResolvedValue({
      ok: true,
      status: 201,
      headers: new Headers({ "content-type": "application/json" }),
      text: async () =>
        JSON.stringify({
          id: "chat_sess_1",
          title: "New Chat",
          createdAt: "2026-03-15T10:00:00.000Z",
          updatedAt: "2026-03-15T10:00:00.000Z",
          lastMessageAt: null,
        }),
    });
    vi.stubGlobal("fetch", fetchMock);

    const response = await postSession(
      new Request("http://localhost/api/chat/guest/sessions", {
        method: "POST",
        headers: {
          "content-type": "application/json",
          origin: "http://localhost",
          referer: "http://localhost/",
          cookie: `hive_guest_session=${cookieValue}`,
        },
        body: JSON.stringify({ title: "My chat" }),
      }),
    );

    expect(fetchMock).toHaveBeenCalledWith(
      "http://api:8080/v1/internal/guest/chat/sessions",
      expect.objectContaining({
        method: "POST",
        body: JSON.stringify({ title: "My chat" }),
      }),
    );
    expect(response.status).toBe(201);
  });

  it("GET /api/chat/guest/sessions/[sessionId] proxies to internal API", async () => {
    const { cookieValue } = createGuestSession("test-web-token");
    const fetchMock = vi.fn().mockResolvedValue({
      ok: true,
      status: 200,
      headers: new Headers({ "content-type": "application/json" }),
      text: async () =>
        JSON.stringify({
          id: "chat_sess_abc",
          title: "Guest chat",
          createdAt: "2026-03-15T10:00:00.000Z",
          updatedAt: "2026-03-15T10:05:00.000Z",
          lastMessageAt: "2026-03-15T10:05:00.000Z",
          messages: [],
        }),
    });
    vi.stubGlobal("fetch", fetchMock);

    const response = await getSessionById(
      new Request("http://localhost/api/chat/guest/sessions/chat_sess_abc", {
        method: "GET",
        ...requestOptions(cookieValue),
      }),
      { params: Promise.resolve({ sessionId: "chat_sess_abc" }) },
    );

    expect(fetchMock).toHaveBeenCalledWith(
      "http://api:8080/v1/internal/guest/chat/sessions/chat_sess_abc",
      expect.objectContaining({
        method: "GET",
        headers: expect.objectContaining({
          "x-web-guest-token": "test-web-token",
          "x-guest-id": expect.stringMatching(/^guest_[a-f0-9]+$/),
        }),
      }),
    );
    expect(response.status).toBe(200);
  });

  it("POST /api/chat/guest/sessions/[sessionId]/messages proxies send with client IP", async () => {
    const { cookieValue } = createGuestSession("test-web-token");
    const fetchMock = vi.fn().mockResolvedValue({
      ok: true,
      status: 200,
      headers: new Headers({ "content-type": "application/json" }),
      text: async () =>
        JSON.stringify({
          id: "chat_sess_1",
          title: "Hello",
          messages: [
            { id: "m1", role: "user", content: "Hello", sequence: 1, createdAt: "", sessionId: "chat_sess_1" },
            { id: "m2", role: "assistant", content: "Hi", sequence: 2, createdAt: "", sessionId: "chat_sess_1" },
          ],
        }),
    });
    vi.stubGlobal("fetch", fetchMock);

    const response = await postMessage(
      new Request("http://localhost/api/chat/guest/sessions/chat_sess_1/messages", {
        method: "POST",
        headers: {
          "content-type": "application/json",
          origin: "http://localhost",
          referer: "http://localhost/",
          cookie: `hive_guest_session=${cookieValue}`,
          "x-forwarded-for": "203.0.113.10",
        },
        body: JSON.stringify({ content: "Hello", model: "guest-free" }),
      }),
      { params: Promise.resolve({ sessionId: "chat_sess_1" }) },
    );

    expect(fetchMock).toHaveBeenCalledWith(
      "http://api:8080/v1/internal/guest/chat/sessions/chat_sess_1/messages",
      expect.objectContaining({
        method: "POST",
        headers: expect.objectContaining({
          "x-web-guest-token": "test-web-token",
          "x-forwarded-for": "203.0.113.10",
        }),
        body: JSON.stringify({ content: "Hello", model: "guest-free" }),
      }),
    );
    expect(response.status).toBe(200);
  });

  it("rejects when guest session cookie is missing", async () => {
    const fetchMock = vi.fn();
    vi.stubGlobal("fetch", fetchMock);

    const response = await getSessions(
      new Request("http://localhost/api/chat/guest/sessions", {
        method: "GET",
        headers: { origin: "http://localhost", referer: "http://localhost/" },
      }),
    );

    expect(fetchMock).not.toHaveBeenCalled();
    expect(response.status).toBe(401);
    await expect(response.json()).resolves.toEqual({ error: "missing guest session" });
  });
});
