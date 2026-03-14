import { useEffect, useMemo, useReducer, useRef, useState } from "react";
import { toast } from "sonner";

import { chatReducer, createInitialChatState } from "../../app/chat/chat-reducer";
import type { ChatMessage } from "../../app/chat/chat-types";
import { useAuthSessionState } from "../auth/auth-session";
import {
  isGuestSessionExpired,
  readGuestSession,
  writeGuestSession,
  type GuestSession,
} from "../auth/guest-session";
import { apiHeaders, getApiBase, getAppUrl } from "../../lib/api";
import { useSupabaseAuthSessionSync } from "../../lib/supabase-client";

export type ChatModelOption = {
  id: string;
  capability: "chat" | "image";
  costType: "free" | "fixed" | "variable";
  locked: boolean;
  lockReason?: string;
};

const DEFAULT_GUEST_MODEL_OPTIONS: ChatModelOption[] = [
  { id: "guest-free", capability: "chat", costType: "free", locked: false },
];

export function useChatSession() {
  useSupabaseAuthSessionSync();
  const { ready: authReady, session: authSession } = useAuthSessionState();
  const [chatState, dispatch] = useReducer(chatReducer, undefined, createInitialChatState);
  const [modelOptions, setModelOptions] = useState<ChatModelOption[]>(DEFAULT_GUEST_MODEL_OPTIONS);
  const [model, setModel] = useState("guest-free");
  const [prompt, setPrompt] = useState("");
  const [loading, setLoading] = useState(false);
  const [errorMessage, setErrorMessage] = useState<string | null>(null);
  const [authModalOpen, setAuthModalOpen] = useState(false);
  const accessToken = authSession?.accessToken ?? "";
  const guestMode = authReady && accessToken.trim().length === 0;
  const guestSessionRefreshedRef = useRef(false);
  const guestSessionRequestRef = useRef<Promise<GuestSession | null> | null>(null);

  async function ensureGuestSession(forceRefresh = false): Promise<GuestSession | null> {
    const currentSession = readGuestSession();
    if (!forceRefresh && guestSessionRefreshedRef.current && !isGuestSessionExpired(currentSession)) {
      return currentSession;
    }
    if (guestSessionRequestRef.current) {
      return guestSessionRequestRef.current;
    }

    const request = (async () => {
      try {
        const response = await fetch(getAppUrl("/api/guest-session"), {
          method: "POST",
        });
        if (!response.ok) {
          return !isGuestSessionExpired(currentSession) ? currentSession : null;
        }

        const session = await response.json() as GuestSession;
        writeGuestSession(session);
        return session;
      } catch {
        return !isGuestSessionExpired(currentSession) ? currentSession : null;
      } finally {
        guestSessionRefreshedRef.current = true;
        guestSessionRequestRef.current = null;
      }
    })();

    guestSessionRequestRef.current = request;
    return request;
  }

  const activeConversation = useMemo(
    () =>
      chatState.conversations.find((conversation) => conversation.id === chatState.activeConversationId) ??
      chatState.conversations[0],
    [chatState.activeConversationId, chatState.conversations],
  );

  useEffect(() => {
    let cancelled = false;

    if (!authReady) {
      return () => {
        cancelled = true;
      };
    }

    const resetGuestSafeModels = () => {
      if (cancelled || !guestMode) {
        return;
      }

      setModelOptions(DEFAULT_GUEST_MODEL_OPTIONS);
      setModel("guest-free");
    };

    resetGuestSafeModels();

    async function loadModels() {
      try {
        const response = await fetch(`${getApiBase()}/v1/models`);
        if (!response.ok) {
          resetGuestSafeModels();
          return;
        }

        const json = await response.json() as {
          data?: Array<{ id?: string; capability?: "chat" | "image"; costType?: "free" | "fixed" | "variable" }>;
        };
        const chatModels = (json.data ?? [])
          .filter((entry) => entry.capability === "chat")
          .map((entry) => ({
            id: entry.id,
            capability: entry.capability,
            costType: entry.costType,
          }))
          .filter((entry): entry is { id: string; capability: "chat"; costType: "free" | "fixed" | "variable" } =>
            Boolean(entry.id && entry.capability && entry.costType))
          .map((entry) => ({
            ...entry,
            locked: guestMode && entry.costType !== "free",
            lockReason: guestMode && entry.costType !== "free" ? "Requires account and credits" : undefined,
          }));

        if (!cancelled && chatModels.length > 0) {
          setModelOptions(chatModels);
          setModel((currentModel) => {
            const selectedModel = chatModels.find((entry) => entry.id === currentModel);
            if (selectedModel && !selectedModel.locked) {
              return selectedModel.id;
            }

            const firstUnlockedModel = chatModels.find((entry) => !entry.locked);
            return firstUnlockedModel?.id ?? chatModels[0].id;
          });
        }
      } catch {
        resetGuestSafeModels();
      }
    }

    void loadModels();

    return () => {
      cancelled = true;
    };
  }, [authReady, guestMode]);

  useEffect(() => {
    if (!authReady) {
      return;
    }

    if (!guestMode) {
      guestSessionRefreshedRef.current = false;
      guestSessionRequestRef.current = null;
      setAuthModalOpen(false);
      return;
    }

    guestSessionRefreshedRef.current = false;
    guestSessionRequestRef.current = null;
    void ensureGuestSession(true);
  }, [authReady, guestMode]);

  function handleModelChange(nextModelId: string) {
    const nextModel = modelOptions.find((option) => option.id === nextModelId);
    if (!nextModel) {
      return;
    }
    if (guestMode && nextModel.locked) {
      setAuthModalOpen(true);
      return;
    }

    setModel(nextModel.id);
  }

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
    if (!authReady || !activeConversation || !prompt.trim()) {
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
    authModalOpen,
    openAuthModal: () => setAuthModalOpen(true),
    closeAuthModal: () => setAuthModalOpen(false),
    onModelChange: handleModelChange,
  };
}
