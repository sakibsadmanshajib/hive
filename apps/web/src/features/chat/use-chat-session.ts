import { FormEvent, useMemo, useReducer, useState } from "react";
import { toast } from "sonner";

import { chatReducer, createInitialChatState } from "../../app/chat/chat-reducer";
import type { ChatMessage } from "../../app/chat/chat-types";

const apiBase = process.env.NEXT_PUBLIC_API_BASE_URL ?? "http://127.0.0.1:8080";

export function useChatSession() {
  const [chatState, dispatch] = useReducer(chatReducer, undefined, createInitialChatState);
  const [apiKey, setApiKey] = useState("");
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [name, setName] = useState("");
  const [model, setModel] = useState("fast-chat");
  const [prompt, setPrompt] = useState("");
  const [loading, setLoading] = useState(false);
  const [authMessage, setAuthMessage] = useState("No API key yet.");
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

  async function registerUser(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setLoading(true);
    setErrorMessage(null);
    try {
      const response = await fetch(`${apiBase}/v1/users/register`, {
        method: "POST",
        headers: { "content-type": "application/json" },
        body: JSON.stringify({ email, password, name }),
      });
      const json = await response.json();
      if (!response.ok) {
        const nextMessage = json.error ?? "Registration failed";
        setAuthMessage(nextMessage);
        toast.error(nextMessage);
        return;
      }
      setApiKey(json.api_key);
      const nextMessage = `Registered ${json.user.email}`;
      setAuthMessage(nextMessage);
      toast.success(nextMessage);
    } finally {
      setLoading(false);
    }
  }

  async function loginUser(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setLoading(true);
    setErrorMessage(null);
    try {
      const response = await fetch(`${apiBase}/v1/users/login`, {
        method: "POST",
        headers: { "content-type": "application/json" },
        body: JSON.stringify({ email, password }),
      });
      const json = await response.json();
      if (!response.ok) {
        const nextMessage = json.error ?? "Login failed";
        setAuthMessage(nextMessage);
        toast.error(nextMessage);
        return;
      }
      setApiKey(json.api_key);
      const nextMessage = `Logged in as ${json.user.email}`;
      setAuthMessage(nextMessage);
      toast.success(nextMessage);
    } finally {
      setLoading(false);
    }
  }

  async function sendMessage() {
    if (!activeConversation || !prompt.trim()) {
      return;
    }
    if (!apiKey.trim()) {
      const nextError = "Set API key first.";
      setAuthMessage(nextError);
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
    apiKey,
    setApiKey,
    email,
    setEmail,
    password,
    setPassword,
    name,
    setName,
    model,
    setModel,
    prompt,
    setPrompt,
    loading,
    authMessage,
    errorMessage,
    registerUser,
    loginUser,
    sendMessage,
  };
}
