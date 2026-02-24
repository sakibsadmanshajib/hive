import type { ChatAction, ChatConversation, ChatState } from "./chat-types";

function updateConversation(
  conversations: ChatConversation[],
  id: string,
  updater: (conversation: ChatConversation) => ChatConversation,
): ChatConversation[] {
  return conversations.map((conversation) => (conversation.id === id ? updater(conversation) : conversation));
}

export function createInitialChatState(): ChatState {
  const initialConversation: ChatConversation = {
    id: "conv_1",
    title: "New Chat",
    messages: [{ role: "assistant", content: "Welcome back. What's on the agenda today?", createdAt: new Date().toISOString() }],
  };

  return {
    conversations: [initialConversation],
    activeConversationId: initialConversation.id,
  };
}

export function chatReducer(state: ChatState, action: ChatAction): ChatState {
  switch (action.type) {
    case "conversationAdded": {
      const conversation: ChatConversation = {
        id: action.payload.id,
        title: "Untitled",
        messages: [{ role: "assistant", content: "Start your new conversation.", createdAt: new Date().toISOString() }],
      };
      return {
        conversations: [conversation, ...state.conversations],
        activeConversationId: conversation.id,
      };
    }
    case "conversationSelected": {
      return {
        ...state,
        activeConversationId: action.payload.conversationId,
      };
    }
    case "userMessageQueued": {
      return {
        ...state,
        conversations: updateConversation(state.conversations, action.payload.conversationId, (conversation) => {
          const nextMessages = [...conversation.messages, action.payload.message];
          return {
            ...conversation,
            title: conversation.title === "Untitled" ? action.payload.message.content.slice(0, 32) : conversation.title,
            messages: nextMessages,
          };
        }),
      };
    }
    case "assistantMessageReceived": {
      return {
        ...state,
        conversations: updateConversation(state.conversations, action.payload.conversationId, (conversation) => ({
          ...conversation,
          messages: [...conversation.messages, action.payload.message],
        })),
      };
    }
  }
}
