import { randomUUID } from "node:crypto";
import type { SupabaseClient } from "@supabase/supabase-js";
import type {
  PersistedChatMessage,
  PersistedChatSession,
  PersistedChatSessionSummary,
} from "../domain/types";

type SessionSummaryRow = {
  id: string;
  title: string;
  created_at: string;
  updated_at: string;
  last_message_at: string | null;
};

type SessionRow = SessionSummaryRow & {
  messages?: Array<{
    id: string;
    role: "system" | "user" | "assistant";
    content: string;
    sequence_number: number;
    created_at: string;
  }>;
};

function mapSessionSummary(row: SessionSummaryRow): PersistedChatSessionSummary {
  return {
    id: row.id,
    title: row.title,
    createdAt: new Date(row.created_at).toISOString(),
    updatedAt: new Date(row.updated_at).toISOString(),
    lastMessageAt: row.last_message_at ? new Date(row.last_message_at).toISOString() : null,
  };
}

function mapMessage(sessionId: string, row: NonNullable<SessionRow["messages"]>[number]): PersistedChatMessage {
  return {
    id: row.id,
    sessionId,
    role: row.role,
    content: row.content,
    createdAt: new Date(row.created_at).toISOString(),
    sequence: row.sequence_number,
  };
}

function isNotFoundError(error: { code?: string } | null): boolean {
  return error?.code === "PGRST116";
}

export class SupabaseChatHistoryStore {
  constructor(private readonly supabase: SupabaseClient) {}

  async createSession(input: {
    userId?: string;
    guestId?: string;
    title?: string;
  }): Promise<PersistedChatSessionSummary> {
    const id = `chat_sess_${randomUUID()}`;
    const { data, error } = await this.supabase
      .from("chat_sessions")
      .insert({
        id,
        user_id: input.userId ?? null,
        guest_id: input.guestId ?? null,
        title: input.title?.trim() || "New Chat",
      })
      .select("id,title,created_at,updated_at,last_message_at")
      .single();

    if (error) {
      throw new Error(`failed to create chat session: ${error.message}`);
    }

    return mapSessionSummary(data as SessionSummaryRow);
  }

  async listSessionsForUser(userId: string): Promise<PersistedChatSessionSummary[]> {
    const { data, error } = await this.supabase
      .from("chat_sessions")
      .select("id,title,created_at,updated_at,last_message_at")
      .eq("user_id", userId)
      .order("updated_at", { ascending: false });

    if (error) {
      throw new Error(`failed to list chat sessions: ${error.message}`);
    }

    return (data ?? []).map((row) => mapSessionSummary(row as SessionSummaryRow));
  }

  async listSessionsForGuest(guestId: string): Promise<PersistedChatSessionSummary[]> {
    const { data, error } = await this.supabase
      .from("chat_sessions")
      .select("id,title,created_at,updated_at,last_message_at")
      .eq("guest_id", guestId)
      .is("user_id", null)
      .order("updated_at", { ascending: false });

    if (error) {
      throw new Error(`failed to list guest chat sessions: ${error.message}`);
    }

    return (data ?? []).map((row) => mapSessionSummary(row as SessionSummaryRow));
  }

  async getSessionForGuest(sessionId: string, guestId: string): Promise<PersistedChatSession | null> {
    const { data: sessionData, error: sessionError } = await this.supabase
      .from("chat_sessions")
      .select("id,title,created_at,updated_at,last_message_at")
      .eq("id", sessionId)
      .eq("guest_id", guestId)
      .single();

    if (sessionError) {
      if (isNotFoundError(sessionError)) {
        return null;
      }
      throw new Error(`failed to load guest chat session: ${sessionError.message}`);
    }

    const summaryRow = sessionData as SessionSummaryRow;
    const { data: messagesData, error: messagesError } = await this.supabase
      .from("chat_messages")
      .select("id,role,content,sequence_number,created_at")
      .eq("session_id", sessionId)
      .order("sequence_number", { ascending: true });

    if (messagesError) {
      throw new Error(`failed to load guest chat messages: ${messagesError.message}`);
    }

    const messages = (messagesData ?? []).map((msg) =>
      mapMessage(summaryRow.id, msg as NonNullable<SessionRow["messages"]>[number]),
    );
    return {
      ...mapSessionSummary(summaryRow),
      messages,
    };
  }

  async claimGuestSessionsForUser(guestId: string, userId: string): Promise<void> {
    const { error } = await this.supabase
      .from("chat_sessions")
      .update({ user_id: userId })
      .eq("guest_id", guestId);

    if (error) {
      throw new Error(`failed to claim guest chat sessions: ${error.message}`);
    }
  }

  async getSessionForUser(sessionId: string, userId: string): Promise<PersistedChatSession | null> {
    const { data: sessionData, error: sessionError } = await this.supabase
      .from("chat_sessions")
      .select("id,title,created_at,updated_at,last_message_at")
      .eq("id", sessionId)
      .eq("user_id", userId)
      .single();

    if (sessionError) {
      if (isNotFoundError(sessionError)) {
        return null;
      }
      throw new Error(`failed to load chat session: ${sessionError.message}`);
    }

    const summaryRow = sessionData as SessionSummaryRow;
    const { data: messagesData, error: messagesError } = await this.supabase
      .from("chat_messages")
      .select("id,role,content,sequence_number,created_at")
      .eq("session_id", sessionId)
      .order("sequence_number", { ascending: true });

    if (messagesError) {
      throw new Error(`failed to load chat messages: ${messagesError.message}`);
    }

    const messages = (messagesData ?? []).map((msg) =>
      mapMessage(summaryRow.id, msg as NonNullable<SessionRow["messages"]>[number]),
    );
    return {
      ...mapSessionSummary(summaryRow),
      messages,
    };
  }

  async appendMessage(input: {
    sessionId: string;
    role: "system" | "user" | "assistant";
    content: string;
    sequence: number;
  }): Promise<PersistedChatMessage> {
    const id = `chat_msg_${randomUUID()}`;
    const { data, error } = await this.supabase
      .from("chat_messages")
      .insert({
        id,
        session_id: input.sessionId,
        role: input.role,
        content: input.content,
        sequence_number: input.sequence,
      })
      .select("created_at")
      .single();

    if (error) {
      throw new Error(`failed to append chat message: ${error.message}`);
    }

    return {
      id,
      sessionId: input.sessionId,
      role: input.role,
      content: input.content,
      createdAt: new Date((data as { created_at: string }).created_at).toISOString(),
      sequence: input.sequence,
    };
  }

  async updateSession(
    sessionId: string,
    input: {
      title?: string;
      updatedAt?: string;
      lastMessageAt?: string | null;
    },
  ): Promise<void> {
    const payload: Record<string, string | null> = {};
    if (input.title !== undefined) {
      payload.title = input.title;
    }
    if (input.updatedAt !== undefined) {
      payload.updated_at = input.updatedAt;
    }
    if (input.lastMessageAt !== undefined) {
      payload.last_message_at = input.lastMessageAt;
    }

    const { error } = await this.supabase
      .from("chat_sessions")
      .update(payload)
      .eq("id", sessionId);

    if (error) {
      throw new Error(`failed to update chat session: ${error.message}`);
    }
  }
}
