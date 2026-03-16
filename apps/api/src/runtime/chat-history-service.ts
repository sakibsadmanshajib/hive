import type {
  PersistedChatSession,
  PersistedChatSessionSummary,
} from "../domain/types";

type ChatCompletionResult =
  | {
    error: string;
    statusCode: 400 | 402 | 403 | 502;
  }
  | {
    statusCode: 200;
    headers: Record<string, string>;
    body: {
      choices?: Array<{
        message?: {
          content?: string;
        };
      }>;
    };
  };

export type ChatHistoryStore = {
  createSession(input: { userId?: string; guestId?: string; title?: string }): Promise<PersistedChatSessionSummary>;
  listSessionsForUser(userId: string): Promise<PersistedChatSessionSummary[]>;
  getSessionForUser(sessionId: string, userId: string): Promise<PersistedChatSession | null>;
  listSessionsForGuest(guestId: string): Promise<PersistedChatSessionSummary[]>;
  getSessionForGuest(sessionId: string, guestId: string): Promise<PersistedChatSession | null>;
  claimGuestSessionsForUser(guestId: string, userId: string): Promise<void>;
  appendMessage(input: {
    sessionId: string;
    role: "system" | "user" | "assistant";
    content: string;
    sequence: number;
  }): Promise<import("../domain/types").PersistedChatMessage>;
  updateSession(
    sessionId: string,
    input: { title?: string; updatedAt?: string; lastMessageAt?: string | null },
  ): Promise<void>;
};

type ChatCompletionExecutor = {
  chatCompletions(
    userId: string,
    modelId: string | undefined,
    messages: Array<{ role: string; content: string }>,
    usageContext: { channel: "web"; apiKeyId?: string },
  ): Promise<ChatCompletionResult>;
  guestChatCompletions?(
    guestId: string,
    modelId: string | undefined,
    messages: Array<{ role: string; content: string }>,
    guestIp?: string,
  ): Promise<ChatCompletionResult>;
};

export class PersistentChatHistoryService {
  constructor(
    private readonly store: ChatHistoryStore,
    private readonly ai: ChatCompletionExecutor,
  ) { }

  listSessions(userId: string): Promise<PersistedChatSessionSummary[]> {
    return this.store.listSessionsForUser(userId);
  }

  createSession(userId: string, input: { title?: string }): Promise<PersistedChatSessionSummary> {
    return this.store.createSession({
      userId,
      title: input.title,
    });
  }

  getSession(userId: string, sessionId: string): Promise<PersistedChatSession | null> {
    return this.store.getSessionForUser(sessionId, userId);
  }

  listSessionsForGuest(guestId: string): Promise<PersistedChatSessionSummary[]> {
    return this.store.listSessionsForGuest(guestId);
  }

  createSessionForGuest(guestId: string, input: { title?: string }): Promise<PersistedChatSessionSummary> {
    return this.store.createSession({ guestId, title: input.title });
  }

  getSessionForGuest(guestId: string, sessionId: string): Promise<PersistedChatSession | null> {
    return this.store.getSessionForGuest(sessionId, guestId);
  }

  claimGuestSessionsForUser(guestId: string, userId: string): Promise<void> {
    return this.store.claimGuestSessionsForUser(guestId, userId);
  }

  async sendMessage(
    userId: string,
    sessionId: string,
    input: { model?: string; content: string },
  ): Promise<
    | { type: "success"; session: PersistedChatSession }
    | { type: "not_found" }
    | { type: "error"; statusCode: 400 | 402 | 403 | 502; error: string }
  > {
    const session = await this.store.getSessionForUser(sessionId, userId);
    if (!session) {
      return { type: "not_found" };
    }

    const nextUserSequence = session.messages.length + 1;
    const userMessage = await this.store.appendMessage({
      sessionId,
      role: "user",
      content: input.content,
      sequence: nextUserSequence,
    });
    const nextTitle = session.title === "New Chat" ? input.content.slice(0, 80) || "New Chat" : session.title;
    await this.store.updateSession(sessionId, {
      title: nextTitle,
      updatedAt: userMessage.createdAt,
      lastMessageAt: userMessage.createdAt,
    });

    const completion = await this.ai.chatCompletions(
      userId,
      input.model,
      [...session.messages, userMessage].map((message) => ({
        role: message.role,
        content: message.content,
      })),
      { channel: "web" },
    );
    if ("error" in completion) {
      return { type: "error", statusCode: completion.statusCode, error: completion.error };
    }

    const assistantContent = completion.body.choices?.[0]?.message?.content ?? "No response";
    const assistantMessage = await this.store.appendMessage({
      sessionId,
      role: "assistant",
      content: assistantContent,
      sequence: nextUserSequence + 1,
    });
    await this.store.updateSession(sessionId, {
      updatedAt: assistantMessage.createdAt,
      lastMessageAt: assistantMessage.createdAt,
    });

    const refreshedSession = await this.store.getSessionForUser(sessionId, userId);
    return { type: "success", session: refreshedSession! };
  }

  async sendMessageForGuest(
    guestId: string,
    sessionId: string,
    input: { model?: string; content: string },
    guestIp?: string,
  ): Promise<
    | { type: "success"; session: PersistedChatSession }
    | { type: "not_found" }
    | { type: "error"; statusCode: 400 | 402 | 403 | 502; error: string }
  > {
    const session = await this.store.getSessionForGuest(sessionId, guestId);
    if (!session) {
      return { type: "not_found" };
    }

    if (!this.ai.guestChatCompletions) {
      return { type: "error", statusCode: 502, error: "guest chat not available" };
    }

    const nextUserSequence = session.messages.length + 1;
    const userMessage = await this.store.appendMessage({
      sessionId,
      role: "user",
      content: input.content,
      sequence: nextUserSequence,
    });
    const nextTitle = session.title === "New Chat" ? input.content.slice(0, 80) || "New Chat" : session.title;
    await this.store.updateSession(sessionId, {
      title: nextTitle,
      updatedAt: userMessage.createdAt,
      lastMessageAt: userMessage.createdAt,
    });

    const completion = await this.ai.guestChatCompletions(
      guestId,
      input.model,
      [...session.messages, userMessage].map((message) => ({
        role: message.role,
        content: message.content,
      })),
      guestIp,
    );
    if ("error" in completion) {
      return { type: "error", statusCode: completion.statusCode, error: completion.error };
    }

    const assistantContent = completion.body.choices?.[0]?.message?.content ?? "No response";
    const assistantMessage = await this.store.appendMessage({
      sessionId,
      role: "assistant",
      content: assistantContent,
      sequence: nextUserSequence + 1,
    });
    await this.store.updateSession(sessionId, {
      updatedAt: assistantMessage.createdAt,
      lastMessageAt: assistantMessage.createdAt,
    });

    const refreshedSession = await this.store.getSessionForGuest(sessionId, guestId);
    return { type: "success", session: refreshedSession! };
  }
}
