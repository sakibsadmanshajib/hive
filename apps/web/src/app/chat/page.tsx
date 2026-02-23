"use client";

import { FormEvent, useMemo, useState } from "react";

const apiBase = process.env.NEXT_PUBLIC_API_BASE_URL ?? "http://127.0.0.1:8080";

type Message = { role: "user" | "assistant"; content: string };
type Conversation = { id: string; title: string; messages: Message[] };

const initialConversation: Conversation = {
  id: "conv_1",
  title: "New Chat",
  messages: [{ role: "assistant", content: "Welcome. Log in or use an API key, then start chatting." }],
};

export default function ChatPage() {
  const [conversations, setConversations] = useState<Conversation[]>([initialConversation]);
  const [activeId, setActiveId] = useState(initialConversation.id);
  const [apiKey, setApiKey] = useState("");
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [name, setName] = useState("");
  const [model, setModel] = useState("fast-chat");
  const [prompt, setPrompt] = useState("");
  const [loading, setLoading] = useState(false);
  const [authMessage, setAuthMessage] = useState("No API key yet.");

  const activeConversation = useMemo(
    () => conversations.find((conversation) => conversation.id === activeId) ?? conversations[0],
    [activeId, conversations],
  );

  function updateConversation(id: string, updater: (conversation: Conversation) => Conversation) {
    setConversations((prev) => prev.map((conversation) => (conversation.id === id ? updater(conversation) : conversation)));
  }

  function addConversation() {
    const id = `conv_${crypto.randomUUID().slice(0, 8)}`;
    const conversation: Conversation = {
      id,
      title: "Untitled",
      messages: [{ role: "assistant", content: "Start your new conversation." }],
    };
    setConversations((prev) => [conversation, ...prev]);
    setActiveId(id);
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

    const userMessage: Message = { role: "user", content: prompt.trim() };
    updateConversation(activeConversation.id, (conversation) => {
      const nextMessages = [...conversation.messages, userMessage];
      return {
        ...conversation,
        title: conversation.title === "Untitled" ? userMessage.content.slice(0, 32) : conversation.title,
        messages: nextMessages,
      };
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
      const assistantMessage: Message = {
        role: "assistant",
        content: json?.choices?.[0]?.message?.content ?? json?.error ?? "No response",
      };
      updateConversation(activeConversation.id, (conversation) => ({
        ...conversation,
        messages: [...conversation.messages, assistantMessage],
      }));
    } finally {
      setLoading(false);
    }
  }

  return (
    <section style={{ display: "grid", gridTemplateColumns: "260px 1fr", gap: 16, minHeight: "80vh" }}>
      <aside style={{ border: "1px solid #d4d4d8", borderRadius: 10, padding: 12 }}>
        <h2 style={{ marginTop: 0 }}>Conversations</h2>
        <button onClick={addConversation} type="button" style={{ width: "100%" }}>
          + New Chat
        </button>
        <div style={{ marginTop: 12, display: "grid", gap: 8 }}>
          {conversations.map((conversation) => (
            <button
              key={conversation.id}
              onClick={() => setActiveId(conversation.id)}
              type="button"
              style={{
                textAlign: "left",
                border: conversation.id === activeId ? "1px solid #0f766e" : "1px solid #d4d4d8",
                background: conversation.id === activeId ? "#f0fdfa" : "white",
                borderRadius: 8,
                padding: 8,
              }}
            >
              {conversation.title}
            </button>
          ))}
        </div>
      </aside>

      <main style={{ border: "1px solid #d4d4d8", borderRadius: 10, padding: 12, display: "grid", gridTemplateRows: "auto 1fr auto" }}>
        <div style={{ display: "grid", gap: 8, gridTemplateColumns: "1fr 1fr" }}>
          <form onSubmit={registerUser} style={{ border: "1px solid #e4e4e7", borderRadius: 8, padding: 8 }}>
            <strong>Register</strong>
            <input placeholder="Name" value={name} onChange={(event) => setName(event.target.value)} style={{ width: "100%", marginTop: 6 }} />
            <input placeholder="Email" value={email} onChange={(event) => setEmail(event.target.value)} style={{ width: "100%", marginTop: 6 }} />
            <input placeholder="Password" type="password" value={password} onChange={(event) => setPassword(event.target.value)} style={{ width: "100%", marginTop: 6 }} />
            <button disabled={loading} type="submit" style={{ marginTop: 6 }}>
              Create account
            </button>
          </form>

          <form onSubmit={loginUser} style={{ border: "1px solid #e4e4e7", borderRadius: 8, padding: 8 }}>
            <strong>Login</strong>
            <input placeholder="Email" value={email} onChange={(event) => setEmail(event.target.value)} style={{ width: "100%", marginTop: 6 }} />
            <input placeholder="Password" type="password" value={password} onChange={(event) => setPassword(event.target.value)} style={{ width: "100%", marginTop: 6 }} />
            <button disabled={loading} type="submit" style={{ marginTop: 6 }}>
              Login
            </button>
          </form>
        </div>

        <p style={{ margin: "8px 0", color: "#0f766e" }}>{authMessage}</p>
        <label style={{ display: "grid", gap: 4 }}>
          API key
          <input value={apiKey} onChange={(event) => setApiKey(event.target.value)} placeholder="sk_live_..." />
        </label>

        <div style={{ border: "1px solid #e4e4e7", borderRadius: 8, padding: 12, marginTop: 12, overflowY: "auto", maxHeight: "45vh" }}>
          {activeConversation?.messages.map((message, index) => (
            <div
              key={`${message.role}-${index}`}
              style={{
                marginBottom: 12,
                display: "flex",
                justifyContent: message.role === "user" ? "flex-end" : "flex-start",
              }}
            >
              <div
                style={{
                  maxWidth: "80%",
                  padding: 10,
                  borderRadius: 10,
                  background: message.role === "user" ? "#0f766e" : "#f4f4f5",
                  color: message.role === "user" ? "white" : "#111827",
                  whiteSpace: "pre-wrap",
                }}
              >
                {message.content}
              </div>
            </div>
          ))}
        </div>

        <div style={{ display: "grid", gap: 8, marginTop: 10 }}>
          <select value={model} onChange={(event) => setModel(event.target.value)}>
            <option value="fast-chat">fast-chat</option>
            <option value="smart-reasoning">smart-reasoning</option>
          </select>
          <textarea
            rows={3}
            value={prompt}
            onChange={(event) => setPrompt(event.target.value)}
            placeholder="Ask something..."
            style={{ width: "100%" }}
          />
          <button onClick={sendMessage} disabled={loading || !prompt.trim()} type="button">
            {loading ? "Sending..." : "Send"}
          </button>
        </div>
      </main>
    </section>
  );
}
