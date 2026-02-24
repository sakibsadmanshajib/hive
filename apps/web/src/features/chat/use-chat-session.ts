import { useMemo, useReducer, useState } from "react";
import { toast } from "sonner";

import { chatReducer, createInitialChatState } from "../../app/chat/chat-reducer";
import type { ChatMessage } from "../../app/chat/chat-types";
import { readAuthSession } from "../auth/auth-session";

const apiBase = process.env.NEXT_PUBLIC_API_BASE_URL ?? "http://127.0.0.1:8080";

export function useChatSession() {
  const [chatState, dispatch] = useReducer(chatReducer, undefined, createInitialChatState);
  const [apiKey] = useState(() => readAuthSession()?.apiKey ?? "");
  const [model, setModel] = useState("fast-chat");
  const [prompt, setPrompt] = useState("");
  const [loading, setLoading] = useState(false);
  const [errorMessage, setErrorMessage] = useState<string | null>(null);

  const activeConversation = useMemo(
    () =>
      chatState.conversations.find((conversation) => conversation.id === chatState.activeConversationId) ??
      chatState.conversations[0],
    [chatState.activeConversationId, chatState.conversations],
  );

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
    if (!apiKey.trim()) {
      const nextError = "Set API key first.";
      setErrorMessage(nextError);
      toast.error(nextError);
      return;
    }

    const userMessage: ChatMessage = { role: "user", content: prompt.trim() };
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
      const payloadMessages = [...activeConversation.messages, userMessage].map((message) => ({
        role: message.role,
        content: message.content,
      }));

      const response = await fetch(`${apiBase}/v1/chat/completions`, {
        method: "POST",
        headers: {
          "content-type": "application/json",
          "x-api-key": apiKey,
        },
        body: JSON.stringify({
          model,
          messages: payloadMessages,
        }),
      });
      const json = await response.json();
      if (!response.ok) {
        const nextError = json?.error ?? "Chat request failed";
        setErrorMessage(nextError);
        toast.error(nextError);
      }

      const assistantMessage: ChatMessage = {
        role: "assistant",
        content: json?.choices?.[0]?.message?.content ?? json?.error ?? "No response",
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
  };
}
