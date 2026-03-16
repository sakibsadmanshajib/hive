import { describe, expect, it, vi } from "vitest";

import { SupabaseChatHistoryStore } from "../../src/runtime/supabase-chat-history-store";

describe("SupabaseChatHistoryStore", () => {
  it("creates chat sessions owned by an authenticated user", async () => {
    const single = vi.fn(async () => ({
      data: {
        id: "chat_sess_123",
        title: "New Chat",
        created_at: "2026-03-15T10:00:00.000Z",
        updated_at: "2026-03-15T10:00:00.000Z",
      },
      error: null,
    }));
    const select = vi.fn(() => ({
      single,
    }));
    const insert = vi.fn(() => ({
      select,
    }));
    const supabase = {
      from: vi.fn((table: string) => {
        if (table !== "chat_sessions") {
          throw new Error(`unexpected table ${table}`);
        }
        return { insert };
      }),
    };

    const store = new SupabaseChatHistoryStore(supabase as never);
    const session = await store.createSession({
      userId: "4be9070e-4fe8-4da1-bda7-d105ec913af4",
    });

    expect(insert).toHaveBeenCalledWith(
      expect.objectContaining({
        user_id: "4be9070e-4fe8-4da1-bda7-d105ec913af4",
        title: "New Chat",
      }),
    );
    expect(session).toEqual({
      id: "chat_sess_123",
      title: "New Chat",
      createdAt: "2026-03-15T10:00:00.000Z",
      updatedAt: "2026-03-15T10:00:00.000Z",
      lastMessageAt: null,
    });
  });

  it("lists chat sessions for one authenticated user ordered by most recent activity", async () => {
    const order = vi.fn(async () => ({
      data: [
        {
          id: "chat_sess_456",
          title: "Most recent",
          created_at: "2026-03-15T11:00:00.000Z",
          updated_at: "2026-03-15T11:05:00.000Z",
          last_message_at: "2026-03-15T11:05:00.000Z",
        },
      ],
      error: null,
    }));
    const eq = vi.fn(() => ({
      order,
    }));
    const select = vi.fn(() => ({
      eq,
    }));
    const supabase = {
      from: vi.fn((table: string) => {
        if (table !== "chat_sessions") {
          throw new Error(`unexpected table ${table}`);
        }
        return { select };
      }),
    };

    const store = new SupabaseChatHistoryStore(supabase as never);
    const sessions = await store.listSessionsForUser("4be9070e-4fe8-4da1-bda7-d105ec913af4");

    expect(eq).toHaveBeenCalledWith("user_id", "4be9070e-4fe8-4da1-bda7-d105ec913af4");
    expect(order).toHaveBeenCalledWith("updated_at", { ascending: false });
    expect(sessions).toEqual([
      {
        id: "chat_sess_456",
        title: "Most recent",
        createdAt: "2026-03-15T11:00:00.000Z",
        updatedAt: "2026-03-15T11:05:00.000Z",
        lastMessageAt: "2026-03-15T11:05:00.000Z",
      },
    ]);
  });

  it("loads one authenticated chat session with its persisted messages", async () => {
    const sessionRow = {
      id: "chat_sess_456",
      title: "Persisted chat",
      created_at: "2026-03-15T11:00:00.000Z",
      updated_at: "2026-03-15T11:05:00.000Z",
      last_message_at: "2026-03-15T11:05:00.000Z",
    };
    const messagesRows = [
      {
        id: "chat_msg_1",
        role: "user",
        content: "hello",
        sequence_number: 1,
        created_at: "2026-03-15T11:01:00.000Z",
      },
      {
        id: "chat_msg_2",
        role: "assistant",
        content: "hi",
        sequence_number: 2,
        created_at: "2026-03-15T11:02:00.000Z",
      },
    ];
    const single = vi.fn(async () => ({ data: sessionRow, error: null }));
    const eqUser = vi.fn(() => ({ single }));
    const eqId = vi.fn(() => ({ eq: eqUser }));
    const selectSessions = vi.fn(() => ({ eq: eqId }));
    const order = vi.fn(async () => ({ data: messagesRows, error: null }));
    const eqSessionId = vi.fn(() => ({ order }));
    const selectMessages = vi.fn(() => ({ eq: eqSessionId }));
    const supabase = {
      from: vi.fn((table: string) => {
        if (table === "chat_sessions") {
          return { select: selectSessions };
        }
        if (table === "chat_messages") {
          return { select: selectMessages };
        }
        throw new Error(`unexpected table ${table}`);
      }),
    };

    const store = new SupabaseChatHistoryStore(supabase as never);
    const session = await store.getSessionForUser("chat_sess_456", "4be9070e-4fe8-4da1-bda7-d105ec913af4");

    expect(eqId).toHaveBeenCalledWith("id", "chat_sess_456");
    expect(eqUser).toHaveBeenCalledWith("user_id", "4be9070e-4fe8-4da1-bda7-d105ec913af4");
    expect(eqSessionId).toHaveBeenCalledWith("session_id", "chat_sess_456");
    expect(session).toEqual({
      id: "chat_sess_456",
      title: "Persisted chat",
      createdAt: "2026-03-15T11:00:00.000Z",
      updatedAt: "2026-03-15T11:05:00.000Z",
      lastMessageAt: "2026-03-15T11:05:00.000Z",
      messages: [
        {
          id: "chat_msg_1",
          sessionId: "chat_sess_456",
          role: "user",
          content: "hello",
          createdAt: "2026-03-15T11:01:00.000Z",
          sequence: 1,
        },
        {
          id: "chat_msg_2",
          sessionId: "chat_sess_456",
          role: "assistant",
          content: "hi",
          createdAt: "2026-03-15T11:02:00.000Z",
          sequence: 2,
        },
      ],
    });
  });

  it("appends persisted chat messages to an existing session", async () => {
    const single = vi.fn(async () => ({
      data: {
        created_at: "2026-03-15T11:02:00.000Z",
      },
      error: null,
    }));
    const select = vi.fn(() => ({
      single,
    }));
    const insert = vi.fn(() => ({
      select,
    }));
    const supabase = {
      from: vi.fn((table: string) => {
        if (table !== "chat_messages") {
          throw new Error(`unexpected table ${table}`);
        }
        return { insert };
      }),
    };

    const store = new SupabaseChatHistoryStore(supabase as never);
    const message = await store.appendMessage({
      sessionId: "chat_sess_456",
      role: "assistant",
      content: "hi",
      sequence: 2,
    });

    expect(insert).toHaveBeenCalledWith(
      expect.objectContaining({
        session_id: "chat_sess_456",
        role: "assistant",
        content: "hi",
        sequence_number: 2,
      }),
    );
    expect(message).toEqual({
      id: expect.any(String),
      role: "assistant",
      content: "hi",
      createdAt: "2026-03-15T11:02:00.000Z",
      sequence: 2,
      sessionId: "chat_sess_456",
    });
  });

  it("lists chat sessions for one guest ordered by most recent activity", async () => {
    const order = vi.fn(async () => ({
      data: [
        {
          id: "chat_sess_guest_1",
          title: "Guest chat",
          created_at: "2026-03-15T10:00:00.000Z",
          updated_at: "2026-03-15T10:05:00.000Z",
          last_message_at: "2026-03-15T10:05:00.000Z",
        },
      ],
      error: null,
    }));
    const isFilter = vi.fn(() => ({ order }));
    const eq = vi.fn(() => ({ is: isFilter }));
    const select = vi.fn(() => ({ eq }));
    const supabase = {
      from: vi.fn((table: string) => {
        if (table !== "chat_sessions") throw new Error(`unexpected table ${table}`);
        return { select };
      }),
    };

    const store = new SupabaseChatHistoryStore(supabase as never);
    const sessions = await store.listSessionsForGuest("guest_abc");

    expect(eq).toHaveBeenCalledWith("guest_id", "guest_abc");
    expect(isFilter).toHaveBeenCalledWith("user_id", null);
    expect(order).toHaveBeenCalledWith("updated_at", { ascending: false });
    expect(sessions).toEqual([
      {
        id: "chat_sess_guest_1",
        title: "Guest chat",
        createdAt: "2026-03-15T10:00:00.000Z",
        updatedAt: "2026-03-15T10:05:00.000Z",
        lastMessageAt: "2026-03-15T10:05:00.000Z",
      },
    ]);
  });

  it("returns null when loading a guest session that does not exist", async () => {
    const single = vi.fn(async () => ({ data: null, error: { code: "PGRST116" } }));
    const eqGuest = vi.fn(() => ({ single }));
    const eqId = vi.fn(() => ({ eq: eqGuest }));
    const select = vi.fn(() => ({ eq: eqId }));
    const supabase = {
      from: vi.fn((table: string) => {
        if (table !== "chat_sessions") throw new Error(`unexpected table ${table}`);
        return { select };
      }),
    };

    const store = new SupabaseChatHistoryStore(supabase as never);
    const session = await store.getSessionForGuest("chat_sess_missing", "guest_xyz");

    expect(session).toBeNull();
  });

  it("claims all guest-owned sessions for the given user while retaining guest_id", async () => {
    const eq = vi.fn(() => Promise.resolve({ error: null }));
    const update = vi.fn(() => ({ eq }));
    const supabase = {
      from: vi.fn((table: string) => {
        if (table !== "chat_sessions") throw new Error(`unexpected table ${table}`);
        return { update };
      }),
    };

    const store = new SupabaseChatHistoryStore(supabase as never);
    await store.claimGuestSessionsForUser("guest_claim_me", "4be9070e-4fe8-4da1-bda7-d105ec913af4");

    expect(update).toHaveBeenCalledWith({ user_id: "4be9070e-4fe8-4da1-bda7-d105ec913af4" });
    expect(eq).toHaveBeenCalledWith("guest_id", "guest_claim_me");
  });

  it("guest listing excludes sessions that have been linked to a user", async () => {
    const order = vi.fn(async () => ({
      data: [
        {
          id: "chat_sess_unlinked",
          title: "Still guest",
          created_at: "2026-03-15T10:00:00.000Z",
          updated_at: "2026-03-15T10:05:00.000Z",
          last_message_at: "2026-03-15T10:05:00.000Z",
        },
      ],
      error: null,
    }));
    const isFilter = vi.fn(() => ({ order }));
    const eq = vi.fn(() => ({ is: isFilter }));
    const select = vi.fn(() => ({ eq }));
    const supabase = {
      from: vi.fn((table: string) => {
        if (table !== "chat_sessions") throw new Error(`unexpected table ${table}`);
        return { select };
      }),
    };

    const store = new SupabaseChatHistoryStore(supabase as never);
    const sessions = await store.listSessionsForGuest("guest_abc");

    expect(eq).toHaveBeenCalledWith("guest_id", "guest_abc");
    expect(isFilter).toHaveBeenCalledWith("user_id", null);
    expect(order).toHaveBeenCalledWith("updated_at", { ascending: false });
    expect(sessions).toEqual([
      {
        id: "chat_sess_unlinked",
        title: "Still guest",
        createdAt: "2026-03-15T10:00:00.000Z",
        updatedAt: "2026-03-15T10:05:00.000Z",
        lastMessageAt: "2026-03-15T10:05:00.000Z",
      },
    ]);
  });
});
