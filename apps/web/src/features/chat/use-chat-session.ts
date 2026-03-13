import { useEffect, useMemo, useReducer, useState } from "react";
import { toast } from "sonner";

import { chatReducer, createInitialChatState } from "../../app/chat/chat-reducer";
import type { ChatMessage } from "../../app/chat/chat-types";
import { useAuthSession } from "../auth/auth-session";
import {
  isGuestSessionExpired,
  readGuestSession,
  writeGuestSession,
  type GuestSession,
} from "../auth/guest-session";
import { apiHeaders, getApiBase, getAppUrl } from "../../lib/api";
import { useSupabaseAuthSessionSync } from "../../lib/supabase-client";

export function useChatSession() {
  useSupabaseAuthSessionSync();
  const authSession = useAuthSession();
  const [chatState, dispatch] = useReducer(chatReducer, undefined, createInitialChatState);
  const [modelOptions, setModelOptions] = useState(["guest-free"]);
  const [model, setModel] = useState("guest-free");
  const [prompt, setPrompt] = useState("");
  const [loading, setLoading] = useState(false);
  const [errorMessage, setErrorMessage] = useState<string | null>(null);
  const accessToken = authSession?.accessToken ?? "";
  const guestMode = accessToken.trim().length === 0;

  async function ensureGuestSession(): Promise<GuestSession | null> {
    const currentSession = readGuestSession();
    if (!isGuestSessionExpired(currentSession)) {
      return currentSession;
    }

    try {
      const response = await fetch(getAppUrl("/api/guest-session"), {
        method: "POST",
      });
      if (!response.ok) {
        return null;
      }

      const session = await response.json() as GuestSession;
      writeGuestSession(session);
      return session;
    } catch {
      return null;
    }
  }

  const activeConversation = useMemo(
    () =>
      chatState.conversations.find((conversation) => conversation.id === chatState.activeConversationId) ??
      chatState.conversations[0],
    [chatState.activeConversationId, chatState.conversations],
  );

  useEffect(() => {
    let cancelled = false;

    async function loadModels() {
      try {
        const response = await fetch(`${getApiBase()}/v1/models`);
        if (!response.ok) {
          return;
        }

        const json = await response.json() as {
          data?: Array<{ id?: string; capability?: string; costType?: string }>;
        };
        const chatModels = (json.data ?? [])
          .filter((entry) => entry.capability === "chat")
          .filter((entry) => !guestMode || entry.costType === "free")
          .map((entry) => entry.id)
          .filter((entry): entry is string => Boolean(entry));

        if (!cancelled && chatModels.length > 0) {
          setModelOptions(chatModels);
          setModel((currentModel) => (chatModels.includes(currentModel) ? currentModel : chatModels[0]));
        }
      } catch {
        // Keep built-in model defaults if the catalog request fails.
      }
    }

    void loadModels();

    return () => {
      cancelled = true;
    };
  }, [guestMode]);

  useEffect(() => {
    if (!guestMode) {
      return;
    }

    void ensureGuestSession();
  }, [guestMode]);

  function addConversation() {
    dispatch({
      type: "conversationAdded",
      payload: {
        id: `conv_${crypto.randomUUID().slice(0, 8)}`,
      },
    });
  }

  function selectConversation(conversationId: string) {
    dispatch({
      type: "conversationSelected",
      payload: { conversationId },
    });
  }

  async function sendMessage() {
    if (!activeConversation || !prompt.trim()) {
      return;
    }

    const userMessage: ChatMessage = {
      role: "user",
      content: prompt.trim(),
      createdAt: new Date().toISOString(),
    };
    dispatch({
      type: "userMessageQueued",
      payload: {
        conversationId: activeConversation.id,
        message: userMessage,
      },
    });
    setPrompt("");
    setLoading(true);
    setErrorMessage(null);

    try {
      const apiBase = getApiBase();
      const payloadMessages = [...activeConversation.messages, userMessage].map((message) => ({
        role: message.role,
        content: message.content,
      }));
      if (guestMode) {
        const guestSession = await ensureGuestSession();
        if (!guestSession) {
          setErrorMessage("Guest chat unavailable");
          toast.error("Guest chat unavailable");
          return;
        }
      }

      const response = await fetch(
        guestMode ? getAppUrl("/api/chat/guest") : `${apiBase}/v1/chat/completions`,
        {
        method: "POST",
        headers: guestMode ? { "content-type": "application/json" } : apiHeaders(accessToken),
        body: JSON.stringify({
          model,
          messages: payloadMessages,
        }),
        },
      );
      const json = await response.json();
      if (!response.ok) {
        const nextError = json?.error ?? "Chat request failed";
        setErrorMessage(nextError);
        toast.error(nextError);
        return;
      }

      const assistantMessage: ChatMessage = {
        role: "assistant",
        content: json?.choices?.[0]?.message?.content ?? json?.error ?? "No response",
        createdAt: new Date().toISOString(),
      };
      dispatch({
        type: "assistantMessageReceived",
        payload: {
          conversationId: activeConversation.id,
          message: assistantMessage,
        },
      });
      toast.success("Reply received");
    } catch (error) {
      const nextError = error instanceof Error ? error.message : "Unexpected chat error";
      setErrorMessage(nextError);
      toast.error(nextError);
    } finally {
      setLoading(false);
    }
  }

  return {
    conversations: chatState.conversations,
    activeConversation,
    activeConversationId: chatState.activeConversationId,
    addConversation,
    selectConversation,
    model,
    setModel,
    prompt,
    setPrompt,
    loading,
    errorMessage,
    sendMessage,
    modelOptions,
    guestMode,
  };
}
