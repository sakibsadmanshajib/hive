export type ChatRole = "user" | "assistant";

export type ChatMessage = {
  role: ChatRole;
  content: string;
  createdAt: string;
};

export type ChatConversation = {
  id: string;
  title: string;
  messages: ChatMessage[];
};

export type ChatState = {
  conversations: ChatConversation[];
  activeConversationId: string;
};

export type ChatAction =
  | {
      type: "conversationAdded";
      payload: { id: string };
    }
  | {
      type: "conversationSelected";
      payload: { conversationId: string };
    }
  | {
      type: "userMessageQueued";
      payload: { conversationId: string; message: ChatMessage };
    }
  | {
      type: "assistantMessageReceived";
      payload: { conversationId: string; message: ChatMessage };
    };
