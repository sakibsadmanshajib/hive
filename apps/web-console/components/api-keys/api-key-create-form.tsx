"use client";

import { useState } from "react";
import { useRouter } from "next/navigation";
import type { ApiKey } from "@/lib/control-plane/client";

export function ApiKeyCreateForm() {
  const [nickname, setNickname] = useState("");
  const [expiresAt, setExpiresAt] = useState("");
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [createdKey, setCreatedKey] = useState<ApiKey | null>(null);
  const [copied, setCopied] = useState(false);
  const router = useRouter();

  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault();
    if (!nickname.trim()) {
      setError("Nickname is required.");
      return;
    }

    setLoading(true);
    setError(null);

    try {
      const body: { nickname: string; expires_at?: string } = {
        nickname: nickname.trim(),
      };
      if (expiresAt) {
        body.expires_at = new Date(expiresAt).toISOString();
      }

      const response = await fetch("/api/v1/accounts/current/api-keys", {
        method: "POST",
        credentials: "include",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(body),
      });

      if (!response.ok) {
        setError("Failed to create key. Please try again.");
        setLoading(false);
        return;
      }

      const data: unknown = await response.json();
      if (
        data !== null &&
        typeof data === "object" &&
        "id" in data
      ) {
        setCreatedKey(data as ApiKey);
        router.refresh();
      } else {
        setError("Failed to create key. Please try again.");
      }
    } catch {
      setError("Failed to create key. Please try again.");
    } finally {
      setLoading(false);
    }
  }

  async function handleCopy() {
    if (!createdKey?.secret) {
      return;
    }
    try {
      await navigator.clipboard.writeText(createdKey.secret);
      setCopied(true);
      setTimeout(() => setCopied(false), 2000);
    } catch {
      // Clipboard API unavailable — user must copy manually
    }
  }

  if (createdKey) {
    return (
      <div
        style={{
          border: "1px solid #d1d5db",
          borderRadius: "0.75rem",
          padding: "1rem",
          display: "grid",
          gap: "1rem",
          maxWidth: "48rem",
        }}
      >
        <h2 style={{ margin: 0, fontSize: "1.25rem", fontWeight: 700 }}>
          API Key Created
        </h2>
        <div
          style={{
            background: "#eff6ff",
            border: "1px solid #93c5fd",
            borderRadius: "0.375rem",
            padding: "0.75rem",
            display: "grid",
            gap: "0.5rem",
          }}
        >
          <p style={{ margin: 0, fontWeight: 700, color: "#1d4ed8" }}>
            This is the only time your key secret will be shown. Copy it now.
          </p>
          <div style={{ display: "flex", alignItems: "center", gap: "0.5rem" }}>
            <code
              style={{
                fontFamily: "monospace",
                background: "#ffffff",
                border: "1px solid #d1d5db",
                borderRadius: "0.25rem",
                padding: "0.375rem 0.5rem",
                fontSize: "0.875rem",
                flex: 1,
                overflowX: "auto",
                wordBreak: "break-all",
              }}
            >
              {createdKey.secret}
            </code>
            <button
              type="button"
              onClick={() => void handleCopy()}
              style={{
                background: "#1d4ed8",
                color: "#ffffff",
                border: "none",
                borderRadius: "0.375rem",
                padding: "0.375rem 0.75rem",
                fontWeight: 700,
                cursor: "pointer",
                fontSize: "0.875rem",
                whiteSpace: "nowrap",
              }}
            >
              {copied ? "Copied!" : "Copy"}
            </button>
          </div>
        </div>
        <div style={{ display: "grid", gap: "0.25rem", fontSize: "0.875rem", color: "#6b7280" }}>
          <span>
            <strong>Nickname:</strong> {createdKey.nickname}
          </span>
          {createdKey.expires_at && (
            <span>
              <strong>Expires:</strong> {new Date(createdKey.expires_at).toLocaleDateString()}
            </span>
          )}
        </div>
        <button
          type="button"
          onClick={() => {
            setCreatedKey(null);
            setNickname("");
            setExpiresAt("");
          }}
          style={{
            background: "transparent",
            color: "#1d4ed8",
            border: "1px solid #1d4ed8",
            padding: "0.5rem 1rem",
            borderRadius: "0.375rem",
            fontWeight: 700,
            cursor: "pointer",
            width: "fit-content",
          }}
        >
          Create another key
        </button>
      </div>
    );
  }

  return (
    <div
      style={{
        border: "1px solid #d1d5db",
        borderRadius: "0.75rem",
        padding: "1rem",
        display: "grid",
        gap: "1rem",
        maxWidth: "48rem",
      }}
    >
      <h2 style={{ margin: 0, fontSize: "1.25rem", fontWeight: 700 }}>Create API Key</h2>
      <form onSubmit={(e) => void handleSubmit(e)} style={{ display: "grid", gap: "1rem" }}>
        <div style={{ display: "grid", gap: "0.25rem" }}>
          <label
            htmlFor="key-nickname"
            style={{ fontWeight: 700, fontSize: "0.875rem", color: "#4b5563" }}
          >
            Nickname <span style={{ color: "#dc2626" }}>*</span>
          </label>
          <input
            id="key-nickname"
            type="text"
            value={nickname}
            onChange={(e) => setNickname(e.target.value)}
            placeholder="e.g. production-server"
            required
            style={{
              border: "1px solid #d1d5db",
              borderRadius: "0.375rem",
              padding: "0.5rem",
              width: "100%",
              fontSize: "1rem",
              boxSizing: "border-box",
            }}
          />
        </div>

        <div style={{ display: "grid", gap: "0.25rem" }}>
          <label
            htmlFor="key-expires"
            style={{ fontWeight: 700, fontSize: "0.875rem", color: "#4b5563" }}
          >
            Expiration date{" "}
            <span style={{ fontWeight: 400, color: "#6b7280" }}>(optional)</span>
          </label>
          <input
            id="key-expires"
            type="date"
            value={expiresAt}
            onChange={(e) => setExpiresAt(e.target.value)}
            style={{
              border: "1px solid #d1d5db",
              borderRadius: "0.375rem",
              padding: "0.5rem",
              width: "100%",
              fontSize: "1rem",
              boxSizing: "border-box",
            }}
          />
        </div>

        {error && (
          <p style={{ margin: 0, color: "#dc2626", fontSize: "0.875rem" }}>{error}</p>
        )}

        <button
          type="submit"
          disabled={loading}
          style={{
            background: loading ? "#9ca3af" : "#1d4ed8",
            color: "#ffffff",
            padding: "0.5rem 1rem",
            borderRadius: "0.375rem",
            border: "none",
            fontWeight: 700,
            cursor: loading ? "not-allowed" : "pointer",
            opacity: loading ? 0.7 : 1,
            width: "fit-content",
          }}
        >
          {loading ? "Loading..." : "Create API Key"}
        </button>
      </form>
    </div>
  );
}
