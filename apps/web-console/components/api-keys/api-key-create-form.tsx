"use client";

import { useState, type FormEvent } from "react";
import { useRouter } from "next/navigation";
import { Copy, Plus, Check } from "lucide-react";

import type { ApiKey } from "@/lib/control-plane/client";
import { Button } from "@/components/ui/button";
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import { Field, Input } from "@/components/ui/input";

interface CreateApiKeyResponse {
  id: string;
  nickname: string;
  status: string;
  redacted_suffix: string;
  created_at: string;
  updated_at: string;
  expires_at: string | null;
  last_used_at: string | null;
  expiration_summary: { kind: string; label: string };
  budget_summary: { kind: string; label: string };
  allowlist_summary: { mode: string; group_names: string[]; label: string };
  secret?: string;
}

function isApiKeyResponse(value: unknown): value is CreateApiKeyResponse {
  if (value === null || typeof value !== "object") return false;
  const candidate = value as Record<string, unknown>;
  return (
    typeof candidate.id === "string" &&
    typeof candidate.nickname === "string"
  );
}

export function ApiKeyCreateForm() {
  const [nickname, setNickname] = useState("");
  const [expiresAt, setExpiresAt] = useState("");
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [createdKey, setCreatedKey] = useState<ApiKey | null>(null);
  const [copied, setCopied] = useState(false);
  const router = useRouter();

  async function handleSubmit(e: FormEvent<HTMLFormElement>) {
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
      if (isApiKeyResponse(data)) {
        setCreatedKey(data);
        router.refresh();
      } else {
        setError("Failed to create key. Please try again.");
      }
    } catch (err: unknown) {
      const message =
        err instanceof Error ? err.message : "Failed to create key.";
      setError(message);
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
      // Clipboard API unavailable — user must copy manually.
    }
  }

  if (createdKey) {
    return (
      <Card>
        <CardHeader>
          <CardTitle>Key created — copy it now</CardTitle>
          <CardDescription>
            This is the only time the secret is shown. Store it in a secret
            manager before navigating away.
          </CardDescription>
        </CardHeader>
        <CardContent className="flex flex-col gap-4 px-5 py-5">
          <div className="flex items-center gap-2 rounded-md border border-[var(--color-border)] bg-[var(--color-surface-inset)] px-3 py-2">
            <code
              className="flex-1 overflow-x-auto whitespace-nowrap font-mono text-xs text-[var(--color-ink)]"
              data-testid="created-api-key-secret"
            >
              {createdKey.secret ?? "—"}
            </code>
            <Button
              type="button"
              variant="secondary"
              size="sm"
              onClick={() => void handleCopy()}
            >
              {copied ? (
                <>
                  <Check size={14} aria-hidden="true" /> Copied
                </>
              ) : (
                <>
                  <Copy size={14} aria-hidden="true" /> Copy
                </>
              )}
            </Button>
          </div>
          <dl className="grid grid-cols-2 gap-2 text-xs text-[var(--color-ink-3)]">
            <div className="flex flex-col gap-0.5">
              <dt className="text-2xs uppercase tracking-wider">Nickname</dt>
              <dd className="text-sm text-[var(--color-ink)]">
                {createdKey.nickname}
              </dd>
            </div>
            <div className="flex flex-col gap-0.5">
              <dt className="text-2xs uppercase tracking-wider">Expires</dt>
              <dd className="text-sm text-[var(--color-ink)] tabular-nums">
                {createdKey.expires_at
                  ? new Date(createdKey.expires_at).toLocaleDateString()
                  : "Never"}
              </dd>
            </div>
          </dl>
          <Button
            type="button"
            variant="secondary"
            size="sm"
            onClick={() => {
              setCreatedKey(null);
              setNickname("");
              setExpiresAt("");
            }}
            className="self-start"
          >
            Create another
          </Button>
        </CardContent>
      </Card>
    );
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle>Create API key</CardTitle>
        <CardDescription>
          Each key is scoped to this workspace. Set an expiry to enforce
          rotation cadence.
        </CardDescription>
      </CardHeader>
      <CardContent className="px-5 py-5">
        <form
          onSubmit={(e) => void handleSubmit(e)}
          className="grid gap-4 sm:grid-cols-[1fr_220px_auto] sm:items-end"
        >
          <Field label="Nickname" htmlFor="key-nickname" required>
            <Input
              id="key-nickname"
              type="text"
              value={nickname}
              onChange={(e) => setNickname(e.target.value)}
              placeholder="production-server"
              required
            />
          </Field>
          <Field
            label="Expires"
            htmlFor="key-expires"
            hint="Optional"
          >
            <Input
              id="key-expires"
              type="date"
              value={expiresAt}
              onChange={(e) => setExpiresAt(e.target.value)}
            />
          </Field>
          <Button
            type="submit"
            variant="primary"
            disabled={loading}
            className="sm:self-end"
          >
            <Plus size={14} aria-hidden="true" />
            {loading ? "Creating…" : "Create key"}
          </Button>
          {error ? (
            <p
              role="alert"
              className="text-xs text-[var(--color-danger)] sm:col-span-3"
            >
              {error}
            </p>
          ) : null}
        </form>
      </CardContent>
    </Card>
  );
}
