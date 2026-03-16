import { describe, expect, it, vi } from "vitest";
import { registerChatSessionsRoute } from "../../src/routes/chat-sessions";

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
    header() {
      return this;
    },
  };
}

function createServices(overrides: Record<string, unknown> = {}) {
  return {
    env: { allowDevApiKeyPrefix: false, supabase: { flags: { authEnabled: true } } },
    supabaseAuth: {
      getSessionPrincipal: async () => ({ userId: "4be9070e-4fe8-4da1-bda7-d105ec913af4" }),
    },
    authz: { requirePermission: async () => true },
    userSettings: { getForUser: async () => ({ apiEnabled: true }), canUse: () => true },
    users: { resolveApiKey: async () => null },
    chatHistory: {
      listSessions: async () => [],
      createSession: async () => ({
        id: "chat_sess_123",
        title: "New Chat",
        createdAt: "2026-03-15T10:00:00.000Z",
        updatedAt: "2026-03-15T10:00:00.000Z",
        lastMessageAt: null,
      }),
      getSession: async () => ({
        id: "chat_sess_123",
        title: "Persisted chat",
        createdAt: "2026-03-15T10:00:00.000Z",
        updatedAt: "2026-03-15T10:05:00.000Z",
        lastMessageAt: "2026-03-15T10:05:00.000Z",
        messages: [
          {
            id: "chat_msg_1",
            role: "user",
            content: "hello",
            createdAt: "2026-03-15T10:01:00.000Z",
            sequence: 1,
          },
        ],
      }),
      sendMessage: async () => ({
        type: "success",
        session: {
          id: "chat_sess_123",
          title: "Persisted chat",
          createdAt: "2026-03-15T10:00:00.000Z",
          updatedAt: "2026-03-15T10:05:00.000Z",
          lastMessageAt: "2026-03-15T10:05:00.000Z",
          messages: [
            {
              id: "chat_msg_1",
              role: "user",
              content: "hello",
              createdAt: "2026-03-15T10:01:00.000Z",
              sequence: 1,
            },
          ],
        },
      }),
    },
    ...overrides,
  };
}

describe("chat sessions route", () => {
  it("registers authenticated chat session routes", () => {
    const app = new FakeApp();

    registerChatSessionsRoute(app as never, createServices() as never);

    expect(app.getHandlers.has("/v1/chat/sessions")).toBe(true);
    expect(app.getHandlers.has("/v1/chat/sessions/:sessionId")).toBe(true);
    expect(app.postHandlers.has("/v1/chat/sessions")).toBe(true);
    expect(app.postHandlers.has("/v1/chat/sessions/:sessionId/messages")).toBe(true);
  });

  it("lists persisted chat sessions for the authenticated browser user", async () => {
    const listSessions = vi.fn(async () => [
      {
        id: "chat_sess_123",
        title: "Most recent",
        createdAt: "2026-03-15T10:00:00.000Z",
        updatedAt: "2026-03-15T10:05:00.000Z",
        lastMessageAt: "2026-03-15T10:05:00.000Z",
      },
    ]);
    const app = new FakeApp();
    registerChatSessionsRoute(app as never, createServices({
      chatHistory: {
        listSessions,
        createSession: async () => undefined,
        getSession: async () => undefined,
        sendMessage: async () => undefined,
      },
    }) as never);

    const handler = app.getHandlers.get("/v1/chat/sessions");
    const reply = createReply();

    const result = await handler?.(
      {
        headers: {
          authorization: "Bearer session-token",
          origin: "http://127.0.0.1:3000",
        },
      },
      reply,
    );

    expect(listSessions).toHaveBeenCalledWith("4be9070e-4fe8-4da1-bda7-d105ec913af4");
    expect(result).toEqual({
      object: "list",
      data: [
        {
          id: "chat_sess_123",
          title: "Most recent",
          createdAt: "2026-03-15T10:00:00.000Z",
          updatedAt: "2026-03-15T10:05:00.000Z",
          lastMessageAt: "2026-03-15T10:05:00.000Z",
        },
      ],
    });
  });

  it("creates a persisted chat session for the authenticated browser user", async () => {
    const createSession = vi.fn(async () => ({
      id: "chat_sess_123",
      title: "New Chat",
      createdAt: "2026-03-15T10:00:00.000Z",
      updatedAt: "2026-03-15T10:00:00.000Z",
      lastMessageAt: null,
    }));
    const app = new FakeApp();
    registerChatSessionsRoute(app as never, createServices({
      chatHistory: {
        listSessions: async () => [],
        createSession,
        getSession: async () => undefined,
        sendMessage: async () => undefined,
      },
    }) as never);

    const handler = app.postHandlers.get("/v1/chat/sessions");
    const reply = createReply();

    const result = await handler?.(
      {
        headers: {
          authorization: "Bearer session-token",
          origin: "http://127.0.0.1:3000",
        },
        body: {
          title: "New Chat",
        },
      },
      reply,
    );

    expect(createSession).toHaveBeenCalledWith("4be9070e-4fe8-4da1-bda7-d105ec913af4", {
      title: "New Chat",
    });
    expect(reply.statusCode).toBe(201);
    expect(result).toEqual({
      id: "chat_sess_123",
      title: "New Chat",
      createdAt: "2026-03-15T10:00:00.000Z",
      updatedAt: "2026-03-15T10:00:00.000Z",
      lastMessageAt: null,
    });
  });

  it("loads one persisted chat session with its messages", async () => {
    const getSession = vi.fn(async () => ({
      id: "chat_sess_123",
      title: "Persisted chat",
      createdAt: "2026-03-15T10:00:00.000Z",
      updatedAt: "2026-03-15T10:05:00.000Z",
      lastMessageAt: "2026-03-15T10:05:00.000Z",
      messages: [
        {
          id: "chat_msg_1",
          role: "user",
          content: "hello",
          createdAt: "2026-03-15T10:01:00.000Z",
          sequence: 1,
        },
      ],
    }));
    const app = new FakeApp();
    registerChatSessionsRoute(app as never, createServices({
      chatHistory: {
        listSessions: async () => [],
        createSession: async () => undefined,
        getSession,
        sendMessage: async () => undefined,
      },
    }) as never);

    const handler = app.getHandlers.get("/v1/chat/sessions/:sessionId");
    const reply = createReply();

    const result = await handler?.(
      {
        headers: {
          authorization: "Bearer session-token",
          origin: "http://127.0.0.1:3000",
        },
        params: {
          sessionId: "chat_sess_123",
        },
      },
      reply,
    );

    expect(getSession).toHaveBeenCalledWith("4be9070e-4fe8-4da1-bda7-d105ec913af4", "chat_sess_123");
    expect(result).toEqual({
      id: "chat_sess_123",
      title: "Persisted chat",
      createdAt: "2026-03-15T10:00:00.000Z",
      updatedAt: "2026-03-15T10:05:00.000Z",
      lastMessageAt: "2026-03-15T10:05:00.000Z",
      messages: [
        {
          id: "chat_msg_1",
          role: "user",
          content: "hello",
          createdAt: "2026-03-15T10:01:00.000Z",
          sequence: 1,
        },
      ],
    });
  });

  it("persists a new message on an existing chat session and returns the refreshed transcript", async () => {
    const sendMessage = vi.fn(async () => ({
      type: "success",
      session: {
        id: "chat_sess_123",
        title: "hello from route",
        createdAt: "2026-03-15T10:00:00.000Z",
        updatedAt: "2026-03-15T10:06:00.000Z",
        lastMessageAt: "2026-03-15T10:06:00.000Z",
        messages: [
          {
            id: "chat_msg_1",
            role: "user",
            content: "hello from route",
            createdAt: "2026-03-15T10:05:00.000Z",
            sequence: 1,
          },
          {
            id: "chat_msg_2",
            role: "assistant",
            content: "Persisted reply",
            createdAt: "2026-03-15T10:06:00.000Z",
            sequence: 2,
          },
        ],
      },
    }));
    const app = new FakeApp();
    registerChatSessionsRoute(app as never, createServices({
      chatHistory: {
        listSessions: async () => [],
        createSession: async () => undefined,
        getSession: async () => undefined,
        sendMessage,
      },
    }) as never);

    const handler = app.postHandlers.get("/v1/chat/sessions/:sessionId/messages");
    const reply = createReply();

    const result = await handler?.(
      {
        headers: {
          authorization: "Bearer session-token",
          origin: "http://127.0.0.1:3000",
        },
        params: {
          sessionId: "chat_sess_123",
        },
        body: {
          model: "fast-chat",
          content: "hello from route",
        },
      },
      reply,
    );

    expect(sendMessage).toHaveBeenCalledWith("4be9070e-4fe8-4da1-bda7-d105ec913af4", "chat_sess_123", {
      model: "fast-chat",
      content: "hello from route",
    });
    expect(result).toEqual({
      id: "chat_sess_123",
      title: "hello from route",
      createdAt: "2026-03-15T10:00:00.000Z",
      updatedAt: "2026-03-15T10:06:00.000Z",
      lastMessageAt: "2026-03-15T10:06:00.000Z",
      messages: [
        {
          id: "chat_msg_1",
          role: "user",
          content: "hello from route",
          createdAt: "2026-03-15T10:05:00.000Z",
          sequence: 1,
        },
        {
          id: "chat_msg_2",
          role: "assistant",
          content: "Persisted reply",
          createdAt: "2026-03-15T10:06:00.000Z",
          sequence: 2,
        },
      ],
    });
  });

  it("translates error responses from the service correctly", async () => {
    const sendMessage = vi.fn(async () => ({
      type: "error",
      statusCode: 402,
      error: "insufficient credits",
    }));
    const app = new FakeApp();
    registerChatSessionsRoute(app as never, createServices({
      chatHistory: {
        listSessions: async () => [],
        createSession: async () => undefined,
        getSession: async () => undefined,
        sendMessage,
      },
    }) as never);

    const handler = app.postHandlers.get("/v1/chat/sessions/:sessionId/messages");
    const reply = createReply();

    const result = await handler?.(
      {
        headers: {
          authorization: "Bearer session-token",
          origin: "http://127.0.0.1:3000",
        },
        params: {
          sessionId: "chat_sess_123",
        },
        body: {
          model: "fast-chat",
          content: "hello from route",
        },
      },
      reply,
    );

    expect(sendMessage).toHaveBeenCalledWith("4be9070e-4fe8-4da1-bda7-d105ec913af4", "chat_sess_123", {
      model: "fast-chat",
      content: "hello from route",
    });
    expect(reply.statusCode).toBe(402);
    expect(reply.sentPayload).toEqual({ error: "insufficient credits" });
  });

  it("rejects chat session history access for api-key callers", async () => {
    const app = new FakeApp();
    registerChatSessionsRoute(app as never, createServices({
      env: { allowDevApiKeyPrefix: false, supabase: { flags: { authEnabled: false } } },
      users: {
        resolveApiKey: async () => ({
          userId: "4be9070e-4fe8-4da1-bda7-d105ec913af4",
          scopes: ["chat"],
          apiKeyId: "key_123",
        }),
      },
      chatHistory: {
        listSessions: async () => [],
        createSession: async () => undefined,
        getSession: async () => undefined,
        sendMessage: async () => undefined,
      },
    }) as never);

    const handler = app.getHandlers.get("/v1/chat/sessions");
    const reply = createReply();

    await handler?.(
      {
        headers: {
          "x-api-key": "sk_test",
        },
      },
      reply,
    );

    expect(reply.statusCode).toBe(403);
    expect(reply.sentPayload).toEqual({ error: "forbidden" });
  });
});
