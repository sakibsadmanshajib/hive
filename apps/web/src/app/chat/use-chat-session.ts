import { FormEvent, useMemo, useReducer, useState } from "react";

import { chatReducer, createInitialChatState } from "./chat-reducer";
import type { ChatMessage } from "./chat-types";

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
    try {
      const response = await fetch(`${apiBase}/v1/users/register`, {
        method: "POST",
        headers: { "content-type": "application/json" },
        body: JSON.stringify({ email, password, name }),
      });
      const json = await response.json();
      if (!response.ok) {
        setAuthMessage(json.error ?? "Registration failed");
        return;
      }
      setApiKey(json.api_key);
      setAuthMessage(`Registered ${json.user.email}`);
    } finally {
      setLoading(false);
    }
  }

  async function loginUser(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setLoading(true);
    try {
      const response = await fetch(`${apiBase}/v1/users/login`, {
        method: "POST",
        headers: { "content-type": "application/json" },
        body: JSON.stringify({ email, password }),
      });
      const json = await response.json();
      if (!response.ok) {
        setAuthMessage(json.error ?? "Login failed");
        return;
      }
      setApiKey(json.api_key);
      setAuthMessage(`Logged in as ${json.user.email}`);
    } finally {
      setLoading(false);
    }
  }

  async function sendMessage() {
    if (!activeConversation || !prompt.trim()) {
      return;
    }
    if (!apiKey.trim()) {
      setAuthMessage("Set API key first.");
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
    registerUser,
    loginUser,
    sendMessage,
  };
}
