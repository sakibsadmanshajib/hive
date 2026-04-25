"use client";

import { useState } from "react";
import { useRouter } from "next/navigation";

import { Button } from "@/components/ui/button";

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
      const response = await fetch(
        `/api/v1/accounts/current/api-keys/${keyId}/revoke`,
        {
          method: "POST",
          credentials: "include",
          headers: { "Content-Type": "application/json" },
        },
      );

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
    } catch (err: unknown) {
      const message =
        err instanceof Error ? err.message : "Failed to revoke key.";
      setError(message);
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
      <Button
        type="button"
        variant="ghost"
        size="sm"
        onClick={() => setShowPanel(true)}
        className="text-[var(--color-danger)] hover:bg-[var(--color-danger-soft)] hover:text-[var(--color-danger)]"
      >
        Revoke
      </Button>
    );
  }

  return (
    <div className="flex min-w-[260px] flex-col gap-2 rounded-md border border-[var(--color-danger)]/30 bg-[var(--color-danger-soft)] px-3 py-3">
      <p className="text-sm font-semibold text-[var(--color-danger)]">
        Revoke this key?
      </p>
      <p className="text-xs text-[var(--color-ink-2)] leading-relaxed">
        Revoking <strong className="font-semibold">{keyNickname}</strong>{" "}
        immediately blocks all requests using it. This cannot be undone.
      </p>

      {error ? (
        <p role="alert" className="text-xs text-[var(--color-danger)]">
          {error}
        </p>
      ) : null}

      <div className="flex items-center gap-2">
        <Button
          type="button"
          variant="danger"
          size="sm"
          onClick={() => void handleRevoke()}
          disabled={loading}
        >
          {loading ? "Revoking…" : "Revoke key"}
        </Button>
        <Button
          type="button"
          variant="secondary"
          size="sm"
          onClick={handleCancel}
          disabled={loading}
        >
          Keep key
        </Button>
      </div>
    </div>
  );
}
