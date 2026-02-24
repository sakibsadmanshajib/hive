import { describe, expect, it } from "vitest";

import { chatReducer, createInitialChatState } from "../src/app/chat/chat-reducer";
import type { ChatConversation, ChatState } from "../src/app/chat/chat-types";

function createConversation(overrides: Partial<ChatConversation> = {}): ChatConversation {
  return {
    id: "conv_1",
    title: "New Chat",
    messages: [{ role: "assistant", content: "Welcome", createdAt: "2000-01-01T00:00:00.000Z" }],
    ...overrides,
  };
}

function createState(overrides: Partial<ChatState> = {}): ChatState {
  const baseConversation = createConversation();
  return {
    conversations: [baseConversation],
    activeConversationId: baseConversation.id,
    ...overrides,
  };
}

describe("chatReducer", () => {
  it("creates initial state with first conversation active", () => {
    const state = createInitialChatState();

    expect(state.conversations).toHaveLength(1);
    expect(state.activeConversationId).toBe(state.conversations[0]?.id);
    expect(state.conversations[0]?.messages[0]?.role).toBe("assistant");
  });

  it("prepends a new conversation and sets it active", () => {
    const state = createState();

    const next = chatReducer(state, {
      type: "conversationAdded",
      payload: {
        id: "conv_2",
      },
    });

    expect(next.activeConversationId).toBe("conv_2");
    expect(next.conversations[0]?.id).toBe("conv_2");
    expect(next.conversations[0]?.title).toBe("Untitled");
  });

  it("appends user message and updates untitled conversation title", () => {
    const state = createState({
      conversations: [createConversation({ id: "conv_1", title: "Untitled", messages: [] })],
    });

    const next = chatReducer(state, {
      type: "userMessageQueued",
      payload: {
        conversationId: "conv_1",
        message: { role: "user", content: "Tell me a short story", createdAt: "2000-01-01T00:01:00.000Z" },
      },
    });

    expect(next.conversations[0]?.title).toBe("Tell me a short story");
    expect(next.conversations[0]?.messages).toEqual([
      { role: "user", content: "Tell me a short story", createdAt: "2000-01-01T00:01:00.000Z" },
    ]);
  });

  it("appends assistant message without changing existing title", () => {
    const state = createState({
      conversations: [
        createConversation({
          id: "conv_1",
          title: "Support",
          messages: [{ role: "user", content: "Hello", createdAt: "2000-01-01T00:01:00.000Z" }],
        }),
      ],
    });

    const next = chatReducer(state, {
      type: "assistantMessageReceived",
      payload: {
        conversationId: "conv_1",
        message: { role: "assistant", content: "Hi there", createdAt: "2000-01-01T00:02:00.000Z" },
      },
    });

    expect(next.conversations[0]?.title).toBe("Support");
    expect(next.conversations[0]?.messages).toEqual([
      { role: "user", content: "Hello", createdAt: "2000-01-01T00:01:00.000Z" },
      { role: "assistant", content: "Hi there", createdAt: "2000-01-01T00:02:00.000Z" },
    ]);
  });
});
