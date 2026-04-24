"use client";

import { useState } from "react";
import { useRouter } from "next/navigation";

interface RevokeConfirmPanelProps {
  keyId: string;
  keyNickname: string;
  onComplete?: () => void;
  onCancel?: () => void;
}

export function RevokeConfirmPanel({
  keyId,
  keyNickname,
  onComplete,
  onCancel,
}: RevokeConfirmPanelProps) {
  const [showPanel, setShowPanel] = useState(false);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const router = useRouter();

  async function handleRevoke() {
    setLoading(true);
    setError(null);

    try {
      const response = await fetch(`/api/v1/accounts/current/api-keys/${keyId}/revoke`, {
        method: "POST",
        credentials: "include",
        headers: { "Content-Type": "application/json" },
      });

      if (!response.ok) {
        setError("Failed to revoke key. Please try again.");
        setLoading(false);
        return;
      }

      setShowPanel(false);
      router.refresh();
      if (onComplete) {
        onComplete();
      }
    } catch {
      setError("Failed to revoke key. Please try again.");
      setLoading(false);
    }
  }

  function handleCancel() {
    setShowPanel(false);
    setError(null);
    if (onCancel) {
      onCancel();
    }
  }

  if (!showPanel) {
    return (
      <button
        type="button"
        onClick={() => setShowPanel(true)}
        style={{
          background: "transparent",
          color: "#dc2626",
          border: "none",
          padding: 0,
          fontSize: "0.875rem",
          cursor: "pointer",
          fontFamily: "inherit",
        }}
      >
        Revoke
      </button>
    );
  }

  return (
    <div
      style={{
        border: "1px solid #fecaca",
        borderRadius: "0.375rem",
        padding: "0.75rem",
        background: "#fef2f2",
        display: "grid",
        gap: "0.5rem",
        minWidth: "240px",
      }}
    >
      <p style={{ margin: 0, fontWeight: 700, color: "#dc2626" }}>Revoke this key?</p>
      <p style={{ margin: 0, fontSize: "0.875rem", color: "#4b5563" }}>
        Revoking this key immediately blocks all requests using it. This cannot be undone.
      </p>
      <p style={{ margin: 0, fontSize: "0.875rem", color: "#6b7280" }}>
        Key: <strong>{keyNickname}</strong>
      </p>

      {error && (
        <p style={{ margin: 0, color: "#dc2626", fontSize: "0.875rem" }}>{error}</p>
      )}

      <div style={{ display: "flex", gap: "0.5rem" }}>
        <button
          type="button"
          onClick={() => void handleRevoke()}
          disabled={loading}
          style={{
            background: loading ? "#9ca3af" : "#dc2626",
            color: "#ffffff",
            padding: "0.5rem 1rem",
            borderRadius: "0.375rem",
            border: "none",
            fontWeight: 700,
            cursor: loading ? "not-allowed" : "pointer",
            opacity: loading ? 0.7 : 1,
            fontSize: "0.875rem",
          }}
        >
          {loading ? "Loading..." : "Revoke key"}
        </button>
        <button
          type="button"
          onClick={handleCancel}
          disabled={loading}
          style={{
            background: "transparent",
            color: "#1d4ed8",
            border: "1px solid #1d4ed8",
            padding: "0.5rem 1rem",
            borderRadius: "0.375rem",
            fontWeight: 700,
            cursor: "pointer",
            fontSize: "0.875rem",
          }}
        >
          Keep key
        </button>
      </div>
    </div>
  );
}
