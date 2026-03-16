import { describe, expect, it, vi } from "vitest";
import { registerGuestChatSessionsRoute } from "../../src/routes/guest-chat-sessions";

type Handler = (request?: any, reply?: any) => Promise<unknown>;

class FakeApp {
  readonly getHandlers = new Map<string, Handler>();
  readonly postHandlers = new Map<string, Handler>();

  get(path: string, handler: Handler) {
    this.getHandlers.set(path, handler);
  }

  post(path: string, handler: Handler) {
    this.postHandlers.set(path, handler);
  }
}

function createReply() {
  let statusCode = 200;
  let sentPayload: unknown;

  return {
    get statusCode() {
      return statusCode;
    },
    get sentPayload() {
      return sentPayload;
    },
    code(status: number) {
      statusCode = status;
      return this;
    },
    send(payload: unknown) {
      sentPayload = payload;
      return payload;
    },
  };
}

const guestSessionSummary = {
  id: "chat_sess_guest_1",
  title: "Guest chat",
  createdAt: "2026-03-15T10:00:00.000Z",
  updatedAt: "2026-03-15T10:05:00.000Z",
  lastMessageAt: "2026-03-15T10:05:00.000Z" as string | null,
};

const guestSessionFull = {
  ...guestSessionSummary,
  messages: [
    {
      id: "chat_msg_1",
      sessionId: "chat_sess_guest_1",
      role: "user" as const,
      content: "hello",
      createdAt: "2026-03-15T10:01:00.000Z",
      sequence: 1,
    },
  ],
};

function createServices(overrides: Record<string, unknown> = {}) {
  return {
    chatHistory: {
      listSessionsForGuest: vi.fn(async () => [guestSessionSummary]),
      createSessionForGuest: vi.fn(async () => guestSessionSummary),
      getSessionForGuest: vi.fn(async () => guestSessionFull),
      sendMessageForGuest: vi.fn(async () => ({ type: "success" as const, session: guestSessionFull })),
    },
    ...overrides,
  };
}

describe("guest chat sessions route", () => {
  it("registers internal guest chat session endpoints", () => {
    const app = new FakeApp();
    registerGuestChatSessionsRoute(app as never, createServices() as never);

    expect(app.getHandlers.has("/v1/internal/guest/chat/sessions")).toBe(true);
    expect(app.getHandlers.has("/v1/internal/guest/chat/sessions/:sessionId")).toBe(true);
    expect(app.postHandlers.has("/v1/internal/guest/chat/sessions")).toBe(true);
    expect(app.postHandlers.has("/v1/internal/guest/chat/sessions/:sessionId/messages")).toBe(true);
  });

  it("returns 403 when x-web-guest-token is missing", async () => {
    process.env.WEB_INTERNAL_GUEST_TOKEN = "secret";
    const app = new FakeApp();
    registerGuestChatSessionsRoute(app as never, createServices() as never);

    const handler = app.getHandlers.get("/v1/internal/guest/chat/sessions");
    const reply = createReply();

    await handler?.(
      { headers: { "x-guest-id": "guest_123" } },
      reply,
    );

    expect(reply.statusCode).toBe(403);
    expect(reply.sentPayload).toEqual({ error: "forbidden" });
  });

  it("returns 400 when x-guest-id is missing", async () => {
    process.env.WEB_INTERNAL_GUEST_TOKEN = "secret";
    const app = new FakeApp();
    registerGuestChatSessionsRoute(app as never, createServices() as never);

    const handler = app.getHandlers.get("/v1/internal/guest/chat/sessions");
    const reply = createReply();

    await handler?.(
      { headers: { "x-web-guest-token": "secret" } },
      reply,
    );

    expect(reply.statusCode).toBe(400);
    expect(reply.sentPayload).toEqual({ error: "missing guest id" });
  });

  it("lists guest chat sessions with valid token and guest id", async () => {
    process.env.WEB_INTERNAL_GUEST_TOKEN = "secret";
    const listSessionsForGuest = vi.fn(async () => [guestSessionSummary]);
    const app = new FakeApp();
    registerGuestChatSessionsRoute(app as never, createServices({ chatHistory: { listSessionsForGuest } }) as never);

    const handler = app.getHandlers.get("/v1/internal/guest/chat/sessions");
    const reply = createReply();

    const result = await handler?.(
      { headers: { "x-web-guest-token": "secret", "x-guest-id": "guest_abc" } },
      reply,
    );

    expect(reply.statusCode).toBe(200);
    expect(listSessionsForGuest).toHaveBeenCalledWith("guest_abc");
    expect(result).toEqual({ object: "list", data: [guestSessionSummary] });
  });

  it("creates a guest chat session with valid token and guest id", async () => {
    process.env.WEB_INTERNAL_GUEST_TOKEN = "secret";
    const createSessionForGuest = vi.fn(async () => guestSessionSummary);
    const app = new FakeApp();
    registerGuestChatSessionsRoute(app as never, createServices({ chatHistory: { createSessionForGuest } }) as never);

    const handler = app.postHandlers.get("/v1/internal/guest/chat/sessions");
    const reply = createReply();

    await handler?.(
      { headers: { "x-web-guest-token": "secret", "x-guest-id": "guest_abc" }, body: { title: "My chat" } },
      reply,
    );

    expect(reply.statusCode).toBe(201);
    expect(createSessionForGuest).toHaveBeenCalledWith("guest_abc", { title: "My chat" });
    expect(reply.sentPayload).toEqual(guestSessionSummary);
  });

  it("returns 404 when guest session is not found", async () => {
    process.env.WEB_INTERNAL_GUEST_TOKEN = "secret";
    const getSessionForGuest = vi.fn(async () => null);
    const app = new FakeApp();
    registerGuestChatSessionsRoute(app as never, createServices({ chatHistory: { getSessionForGuest } }) as never);

    const handler = app.getHandlers.get("/v1/internal/guest/chat/sessions/:sessionId");
    const reply = createReply();

    await handler?.(
      {
        headers: { "x-web-guest-token": "secret", "x-guest-id": "guest_abc" },
        params: { sessionId: "chat_sess_missing" },
      },
      reply,
    );

    expect(reply.statusCode).toBe(404);
    expect(reply.sentPayload).toEqual({ error: "chat session not found" });
  });

  it("sends message in guest session and returns persisted session", async () => {
    process.env.WEB_INTERNAL_GUEST_TOKEN = "secret";
    const sendMessageForGuest = vi.fn(async () => ({ type: "success" as const, session: guestSessionFull }));
    const app = new FakeApp();
    registerGuestChatSessionsRoute(app as never, createServices({ chatHistory: { sendMessageForGuest } }) as never);

    const handler = app.postHandlers.get("/v1/internal/guest/chat/sessions/:sessionId/messages");
    const reply = createReply();

    const result = await handler?.(
      {
        headers: { "x-web-guest-token": "secret", "x-guest-id": "guest_xyz" },
        params: { sessionId: "chat_sess_guest_1" },
        body: { content: "Hello", model: "guest-free" },
      },
      reply,
    );

    expect(reply.statusCode).toBe(200);
    expect(sendMessageForGuest).toHaveBeenCalledWith(
      "guest_xyz",
      "chat_sess_guest_1",
      { content: "Hello", model: "guest-free" },
      undefined,
    );
    expect(result).toEqual(guestSessionFull);
  });
});
