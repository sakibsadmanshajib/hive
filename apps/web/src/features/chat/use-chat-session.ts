import { useCallback, useEffect, useMemo, useReducer, useRef, useState } from "react";
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
import { apiHeaders, getApiBase, getAppUrl, parseJsonResponse } from "../../lib/api";
import { useSupabaseAuthSessionSync } from "../../lib/supabase-client";

export type ChatModelOption = {
  id: string;
  capability: "chat" | "image";
  costType: "free" | "fixed" | "variable";
  locked: boolean;
  lockReason?: string;
};

const DEFAULT_GUEST_MODEL_OPTIONS: ChatModelOption[] = [
];

type ServerSession = {
  id: string;
  title: string;
  createdAt: string;
  updatedAt: string;
  lastMessageAt: string | null;
  messages?: Array<{ role: string; content: string; createdAt: string }>;
};

export function useChatSession() {
  useSupabaseAuthSessionSync();
  const { ready: authReady, session: authSession } = useAuthSessionState();
  const [chatState, dispatch] = useReducer(chatReducer, undefined, createInitialChatState);
  const [modelOptions, setModelOptions] = useState<ChatModelOption[]>(DEFAULT_GUEST_MODEL_OPTIONS);
  const [model, setModel] = useState("");
  const [prompt, setPrompt] = useState("");
  const [loading, setLoading] = useState(false);
  const [errorMessage, setErrorMessage] = useState<string | null>(null);
  const [authModalOpen, setAuthModalOpen] = useState(false);
  const accessToken = authSession?.accessToken ?? "";
  const guestMode = authReady && accessToken.trim().length === 0;
  const stableSessionIdentity = authSession?.email?.trim() || "unknown";
  const authScopeKey = authReady
    ? (guestMode ? "guest" : `session:${stableSessionIdentity}`)
    : "booting";
  const guestSessionRefreshedRef = useRef(false);
  const guestSessionRequestRef = useRef<Promise<GuestSession | null> | null>(null);
  const previousAuthScopeRef = useRef<string | null>(null);
  const serverSessionMapRef = useRef<Map<string, string>>(new Map());
  const lastLoadedAuthScopeRef = useRef<string | null>(null);
  const [sessionReloadTrigger, setSessionReloadTrigger] = useState(0);
  const guestSessionsLoadedRef = useRef(false);

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
        const parsed = await parseJsonResponse(response);
        if (!parsed.ok) {
          return !isGuestSessionExpired(currentSession) ? currentSession : null;
        }
        const session = parsed.data as GuestSession;
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
      setModel("");
    };

    resetGuestSafeModels();

    async function loadModels() {
      try {
        const response = await fetch(`${getApiBase()}/v1/models`);
        const parsed = await parseJsonResponse(response);
        if (!parsed.ok) {
          resetGuestSafeModels();
          return;
        }
        const json = parsed.data as {
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
          const nextModelOptions = chatModels;

          setModelOptions(nextModelOptions);
          setModel((currentModel) => {
            const selectedModel = nextModelOptions.find((entry) => entry.id === currentModel);
            if (selectedModel && !selectedModel.locked) {
              return selectedModel.id;
            }

            const nextUnlockedModel = nextModelOptions.find((entry) => !entry.locked);
            return nextUnlockedModel?.id ?? "";
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

    if (previousAuthScopeRef.current === null) {
      previousAuthScopeRef.current = authScopeKey;
      return;
    }

    if (previousAuthScopeRef.current !== authScopeKey) {
      dispatch({ type: "stateReset" });
      setPrompt("");
      setLoading(false);
      setErrorMessage(null);
      setAuthModalOpen(false);
    }

    previousAuthScopeRef.current = authScopeKey;
  }, [authReady, authScopeKey]);

  useEffect(() => {
    if (!authReady) {
      return;
    }

    if (!guestMode) {
      guestSessionRefreshedRef.current = false;
      guestSessionRequestRef.current = null;
      guestSessionsLoadedRef.current = false;
      setAuthModalOpen(false);
      return;
    }

    guestSessionRefreshedRef.current = false;
    guestSessionRequestRef.current = null;
    // Do not call ensureGuestSession() here: it would POST a new guest on reload when
    // localStorage is empty, overwriting the cookie and breaking persistence. Session is
    // created when loadGuestSessions gets 401, or when sendMessage/addConversation runs.
  }, [authReady, guestMode]);

  useEffect(() => {
    const handler = () => setSessionReloadTrigger((n) => n + 1);
    window.addEventListener("guest-sessions-linked", handler);
    return () => window.removeEventListener("guest-sessions-linked", handler);
  }, []);

  useEffect(() => {
    if (!authReady || !guestMode || guestSessionsLoadedRef.current) {
      return;
    }
    guestSessionsLoadedRef.current = true;
    let cancelled = false;

    async function loadGuestSessions() {
      try {
        const guestSession = readGuestSession();
        const guestHeaders: Record<string, string> = {};
        if (guestSession?.cookieValue) {
          guestHeaders["x-guest-session"] = guestSession.cookieValue;
        }
        let listResponse = await fetch(getAppUrl("/api/chat/guest/sessions"), {
          method: "GET",
          credentials: "include",
          headers: guestHeaders,
        });
        if (listResponse.status === 401 && !guestSession?.cookieValue) {
          await ensureGuestSession();
          if (cancelled) return;
          const updated = readGuestSession();
          if (updated?.cookieValue) guestHeaders["x-guest-session"] = updated.cookieValue;
          listResponse = await fetch(getAppUrl("/api/chat/guest/sessions"), {
            method: "GET",
            credentials: "include",
            headers: guestHeaders,
          });
        }
        if (!listResponse.ok || cancelled) {
          return;
        }
        const listParsed = await parseJsonResponse(listResponse);
        if (!listParsed.ok) return;
        const listJson = listParsed.data as {
          data?: Array<{ id: string; title: string; createdAt: string; updatedAt: string; lastMessageAt: string | null }>;
        };
        const sessions = listJson.data ?? [];
        if (sessions.length === 0 || cancelled) {
          return;
        }

        const conversations = await Promise.all(
          sessions.slice(0, 20).map(async (session) => {
            const detailResponse = await fetch(getAppUrl(`/api/chat/guest/sessions/${session.id}`), {
              method: "GET",
              credentials: "include",
              headers: guestHeaders,
            });
            if (!detailResponse.ok) {
              return null;
            }
            const detailParsed = await parseJsonResponse(detailResponse);
            if (!detailParsed.ok) return null;
            const detail = detailParsed.data as ServerSession;
            const convId = `conv_${crypto.randomUUID().slice(0, 8)}`;
            serverSessionMapRef.current.set(convId, session.id);
            return {
              id: convId,
              title: detail.title || "New Chat",
              messages: (detail.messages ?? []).map((m) => ({
                role: m.role as "user" | "assistant",
                content: m.content,
                createdAt: m.createdAt,
              })),
            };
          }),
        );

        const validConversations = conversations.filter(
          (c): c is NonNullable<typeof c> => c !== null,
        );

        if (!cancelled && validConversations.length > 0) {
          dispatch({
            type: "sessionsLoaded",
            payload: { conversations: validConversations },
          });
        }
      } catch {
        guestSessionsLoadedRef.current = false;
      }
    }

    void loadGuestSessions();

    return () => {
      cancelled = true;
    };
  }, [authReady, guestMode]);

  useEffect(() => {
    if (!authReady || guestMode || !accessToken) {
      return;
    }
    const currentScope = `${authScopeKey}:${sessionReloadTrigger}`;
    if (lastLoadedAuthScopeRef.current === currentScope) {
      return;
    }
    lastLoadedAuthScopeRef.current = currentScope;
    let cancelled = false;

    async function loadSessions() {
      try {
        const response = await fetch(`${getApiBase()}/v1/chat/sessions`, {
          headers: apiHeaders(accessToken),
        });
        if (!response.ok || cancelled) {
          return;
        }
        const parsed = await parseJsonResponse(response);
        if (!parsed.ok) return;
        const json = parsed.data as {
          data?: Array<{ id: string; title: string; createdAt: string; updatedAt: string; lastMessageAt: string | null }>;
        };
        const sessions = json.data ?? [];
        if (sessions.length === 0 || cancelled) {
          return;
        }

        const conversations = await Promise.all(
          sessions.slice(0, 20).map(async (session) => {
            const detailResponse = await fetch(
              `${getApiBase()}/v1/chat/sessions/${session.id}`,
              { headers: apiHeaders(accessToken) },
            );
            if (!detailResponse.ok) {
              return null;
            }
            const detailParsed = await parseJsonResponse(detailResponse);
            if (!detailParsed.ok) return null;
            const detail = detailParsed.data as ServerSession;
            const convId = `conv_${crypto.randomUUID().slice(0, 8)}`;
            serverSessionMapRef.current.set(convId, session.id);
            return {
              id: convId,
              title: detail.title || "Untitled",
              messages: (detail.messages ?? []).map((m) => ({
                role: m.role as "user" | "assistant",
                content: m.content,
                createdAt: m.createdAt,
              })),
            };
          }),
        );

        const validConversations = conversations.filter(
          (c): c is NonNullable<typeof c> => c !== null && c.messages.length > 0,
        );

        if (!cancelled && validConversations.length > 0) {
          dispatch({
            type: "sessionsLoaded",
            payload: { conversations: validConversations },
          });
        }
      } catch {
        // silently fail
      }
    }

    void loadSessions();

    return () => {
      cancelled = true;
    };
  }, [authReady, guestMode, accessToken, authScopeKey, sessionReloadTrigger]);

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

  function guestApiHeaders(): Record<string, string> {
    const session = readGuestSession();
    const out: Record<string, string> = { "content-type": "application/json" };
    if (session?.cookieValue) out["x-guest-session"] = session.cookieValue;
    return out;
  }

  function addConversation() {
    if (guestMode) {
      void (async () => {
        const convId = `conv_${crypto.randomUUID().slice(0, 8)}`;
        try {
          const response = await fetch(getAppUrl("/api/chat/guest/sessions"), {
            method: "POST",
            headers: guestApiHeaders(),
            credentials: "include",
            body: JSON.stringify({ title: "New Chat" }),
          });
          const parsed = await parseJsonResponse(response);
          if (parsed.ok) {
            const serverSession = parsed.data as { id: string };
            serverSessionMapRef.current.set(convId, serverSession.id);
          }
        } catch {
          // continue with local-only conv
        }
        dispatch({
          type: "conversationAdded",
          payload: { id: convId },
        });
      })();
      return;
    }
    if (accessToken) {
      void (async () => {
        try {
          const response = await fetch(`${getApiBase()}/v1/chat/sessions`, {
            method: "POST",
            headers: apiHeaders(accessToken),
            body: JSON.stringify({ title: "New Chat" }),
          });
          const parsed = await parseJsonResponse(response);
          if (parsed.ok) {
            const serverSession = parsed.data as ServerSession;
            const convId = `conv_${crypto.randomUUID().slice(0, 8)}`;
            serverSessionMapRef.current.set(convId, serverSession.id);
            dispatch({
              type: "conversationAdded",
              payload: { id: convId },
            });
          } else {
            dispatch({
              type: "conversationAdded",
              payload: { id: `conv_${crypto.randomUUID().slice(0, 8)}` },
            });
          }
        } catch {
          dispatch({
            type: "conversationAdded",
            payload: { id: `conv_${crypto.randomUUID().slice(0, 8)}` },
          });
        }
      })();
      return;
    }
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

    const selectedModel = modelOptions.find((option) => option.id === model);
    if (guestMode && (!selectedModel || selectedModel.locked || selectedModel.costType !== "free")) {
      setErrorMessage("Guest chat unavailable");
      toast.error("Guest chat unavailable");
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
      if (guestMode) {
        const guestSession = await ensureGuestSession();
        if (!guestSession) {
          setErrorMessage("Guest chat unavailable");
          toast.error("Guest chat unavailable");
          return;
        }

        let serverSessionId = serverSessionMapRef.current.get(activeConversation.id);
        if (!serverSessionId) {
          const createResponse = await fetch(getAppUrl("/api/chat/guest/sessions"), {
            method: "POST",
            headers: guestApiHeaders(),
            credentials: "include",
            body: JSON.stringify({ title: activeConversation.title || "New Chat" }),
          });
          const createParsed = await parseJsonResponse(createResponse);
          if (!createParsed.ok) {
            setErrorMessage(createParsed.error);
            toast.error(createParsed.error);
            return;
          }
          const created = createParsed.data as { id: string };
          serverSessionId = created.id;
          serverSessionMapRef.current.set(activeConversation.id, serverSessionId);
        }

        const response = await fetch(
          getAppUrl(`/api/chat/guest/sessions/${serverSessionId}/messages`),
          {
            method: "POST",
            headers: guestApiHeaders(),
            credentials: "include",
            body: JSON.stringify({
              model,
              content: userMessage.content,
            }),
          },
        );
        const parsed = await parseJsonResponse(response);
        if (!parsed.ok) {
          setErrorMessage(parsed.error);
          toast.error(parsed.error);
          return;
        }

        const serverSession = parsed.data as ServerSession;
        if (serverSession.messages && serverSession.messages.length > 0) {
          const lastMsg = serverSession.messages[serverSession.messages.length - 1];
          if (lastMsg.role === "assistant") {
            const assistantMessage: ChatMessage = {
              role: "assistant",
              content: lastMsg.content,
              createdAt: lastMsg.createdAt ?? new Date().toISOString(),
            };
            dispatch({
              type: "assistantMessageReceived",
              payload: { conversationId: activeConversation.id, message: assistantMessage },
            });
            toast.success("Reply received");
          }
        } else {
          const assistantMessage: ChatMessage = {
            role: "assistant",
            content: "No response",
            createdAt: new Date().toISOString(),
          };
          dispatch({
            type: "assistantMessageReceived",
            payload: { conversationId: activeConversation.id, message: assistantMessage },
          });
          toast.success("Reply received");
        }
      } else {
        let serverSessionId = serverSessionMapRef.current.get(activeConversation.id);
        if (!serverSessionId) {
          const createResponse = await fetch(`${getApiBase()}/v1/chat/sessions`, {
            method: "POST",
            headers: apiHeaders(accessToken),
            body: JSON.stringify({ title: activeConversation.title || "New Chat" }),
          });
          const createParsed = await parseJsonResponse(createResponse);
          if (!createParsed.ok) {
            setErrorMessage(createParsed.error);
            toast.error(createParsed.error);
            return;
          }
          const created = createParsed.data as ServerSession;
          serverSessionId = created.id;
          serverSessionMapRef.current.set(activeConversation.id, serverSessionId);
        }

        const response = await fetch(
          `${getApiBase()}/v1/chat/sessions/${serverSessionId}/messages`,
          {
            method: "POST",
            headers: apiHeaders(accessToken),
            body: JSON.stringify({
              model,
              content: userMessage.content,
            }),
          },
        );
        const parsed = await parseJsonResponse(response);
        if (!parsed.ok) {
          setErrorMessage(parsed.error);
          toast.error(parsed.error);
          return;
        }

        const serverSession = parsed.data as ServerSession;
        if (serverSession.messages && serverSession.messages.length > 0) {
          const lastMsg = serverSession.messages[serverSession.messages.length - 1];
          if (lastMsg.role === "assistant") {
            const assistantMessage: ChatMessage = {
              role: "assistant",
              content: lastMsg.content,
              createdAt: lastMsg.createdAt,
            };
            dispatch({
              type: "assistantMessageReceived",
              payload: { conversationId: activeConversation.id, message: assistantMessage },
            });
            toast.success("Reply received");
          }
        }
      }
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
