import { describe, expect, it, vi } from "vitest";

import { PersistentChatHistoryService } from "../../src/runtime/chat-history-service";

describe("PersistentChatHistoryService", () => {
  it("persists a user message, generates a reply, and returns the refreshed session", async () => {
    const getSessionForUser = vi
      .fn()
      .mockResolvedValueOnce({
        id: "chat_sess_123",
        title: "New Chat",
        createdAt: "2026-03-15T10:00:00.000Z",
        updatedAt: "2026-03-15T10:00:00.000Z",
        lastMessageAt: null,
        messages: [],
      })
      .mockResolvedValueOnce({
        id: "chat_sess_123",
        title: "hello from history",
        createdAt: "2026-03-15T10:00:00.000Z",
        updatedAt: "2026-03-15T10:01:00.000Z",
        lastMessageAt: "2026-03-15T10:01:00.000Z",
        messages: [
          {
            id: "chat_msg_1",
            sessionId: "chat_sess_123",
            role: "user",
            content: "hello from history",
            createdAt: "2026-03-15T10:00:30.000Z",
            sequence: 1,
          },
          {
            id: "chat_msg_2",
            sessionId: "chat_sess_123",
            role: "assistant",
            content: "Persisted reply",
            createdAt: "2026-03-15T10:01:00.000Z",
            sequence: 2,
          },
        ],
      });
    const appendMessage = vi
      .fn()
      .mockResolvedValueOnce({
        id: "chat_msg_1",
        sessionId: "chat_sess_123",
        role: "user",
        content: "hello from history",
        createdAt: "2026-03-15T10:00:30.000Z",
        sequence: 1,
      })
      .mockResolvedValueOnce({
        id: "chat_msg_2",
        sessionId: "chat_sess_123",
        role: "assistant",
        content: "Persisted reply",
        createdAt: "2026-03-15T10:01:00.000Z",
        sequence: 2,
      });
    const updateSession = vi.fn(async () => undefined);
    const store = {
      createSession: vi.fn(),
      listSessionsForUser: vi.fn(),
      getSessionForUser,
      appendMessage,
      updateSession,
    };
    const ai = {
      chatCompletions: vi.fn(async () => ({
        statusCode: 200,
        headers: {
          "x-model-routed": "fast-chat",
          "x-provider-used": "ollama",
          "x-provider-model": "llama3.1:8b",
          "x-actual-credits": "10",
        },
        body: {
          id: "chatcmpl_123",
          object: "chat.completion",
          created: 1742004060,
          model: "fast-chat",
          choices: [{ message: { role: "assistant", content: "Persisted reply" } }],
        },
      })),
    };

    const service = new PersistentChatHistoryService(store as never, ai as never);
    const session = await service.sendMessage(
      "4be9070e-4fe8-4da1-bda7-d105ec913af4",
      "chat_sess_123",
      {
        model: "fast-chat",
        content: "hello from history",
      },
    );

    expect(ai.chatCompletions).toHaveBeenCalledWith(
      "4be9070e-4fe8-4da1-bda7-d105ec913af4",
      { model: "fast-chat", messages: [{ role: "user", content: "hello from history" }] },
      { channel: "web" },
    );
    expect(appendMessage).toHaveBeenNthCalledWith(1, {
      sessionId: "chat_sess_123",
      role: "user",
      content: "hello from history",
      sequence: 1,
    });
    expect(appendMessage).toHaveBeenNthCalledWith(2, {
      sessionId: "chat_sess_123",
      role: "assistant",
      content: "Persisted reply",
      sequence: 2,
    });
    expect(updateSession).toHaveBeenNthCalledWith(1, "chat_sess_123", {
      title: "hello from history",
      lastMessageAt: "2026-03-15T10:00:30.000Z",
      updatedAt: "2026-03-15T10:00:30.000Z",
    });
    expect(updateSession).toHaveBeenNthCalledWith(2, "chat_sess_123", {
      lastMessageAt: "2026-03-15T10:01:00.000Z",
      updatedAt: "2026-03-15T10:01:00.000Z",
    });
    expect(session).toEqual({
      type: "success",
      session: {
        id: "chat_sess_123",
        title: "hello from history",
        createdAt: "2026-03-15T10:00:00.000Z",
        updatedAt: "2026-03-15T10:01:00.000Z",
        lastMessageAt: "2026-03-15T10:01:00.000Z",
        messages: [
          {
            id: "chat_msg_1",
            sessionId: "chat_sess_123",
            role: "user",
            content: "hello from history",
            createdAt: "2026-03-15T10:00:30.000Z",
            sequence: 1,
          },
          {
            id: "chat_msg_2",
            sessionId: "chat_sess_123",
            role: "assistant",
            content: "Persisted reply",
            createdAt: "2026-03-15T10:01:00.000Z",
            sequence: 2,
          },
        ],
      },
    });
  });

  it("persists a user message, updates session early, and returns an error response on completion failure", async () => {
    const getSessionForUser = vi.fn().mockResolvedValueOnce({
      id: "chat_sess_123",
      title: "New Chat",
      createdAt: "2026-03-15T10:00:00.000Z",
      updatedAt: "2026-03-15T10:00:00.000Z",
      lastMessageAt: null,
      messages: [],
    });
    const appendMessage = vi.fn().mockResolvedValueOnce({
      id: "chat_msg_1",
      sessionId: "chat_sess_123",
      role: "user",
      content: "hello",
      createdAt: "2026-03-15T10:00:30.000Z",
      sequence: 1,
    });
    const updateSession = vi.fn(async () => undefined);
    const store = {
      createSession: vi.fn(),
      listSessionsForUser: vi.fn(),
      getSessionForUser,
      appendMessage,
      updateSession,
    };
    const ai = {
      chatCompletions: vi.fn(async () => ({
        statusCode: 402,
        error: "insufficient credits",
      })),
    };

    const service = new PersistentChatHistoryService(store as never, ai as never);
    const result = await service.sendMessage(
      "4be9070e-4fe8-4da1-bda7-d105ec913af4",
      "chat_sess_123",
      {
        model: "fast-chat",
        content: "hello",
      },
    );

    expect(updateSession).toHaveBeenCalledTimes(1);
    expect(updateSession).toHaveBeenCalledWith("chat_sess_123", {
      title: "hello",
      lastMessageAt: "2026-03-15T10:00:30.000Z",
      updatedAt: "2026-03-15T10:00:30.000Z",
    });
    expect(result).toEqual({
      type: "error",
      statusCode: 402,
      error: "insufficient credits",
    });
  });
});
