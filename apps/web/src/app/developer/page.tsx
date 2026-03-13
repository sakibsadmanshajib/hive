"use client";

import { useEffect, useState } from "react";
import { useRouter } from "next/navigation";

import { Button } from "../../components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "../../components/ui/card";
import { useAuthSessionState } from "../../features/auth/auth-session";
import { UsageCards, type UsageSummary, type UserSnapshot } from "../../features/billing/components/usage-cards";
import { DeveloperShell } from "../../features/developer/components/developer-shell";
import { apiHeaders, getApiBase } from "../../lib/api";
import { useSupabaseAuthSessionSync } from "../../lib/supabase-client";

const DEFAULT_SCOPES = ["chat", "image", "usage", "billing"] as const;

function toExpiry(days: string): { expiresAt?: string; error?: string } {
  const trimmed = days.trim();
  if (trimmed === "") {
    return {};
  }
  const parsed = Number(trimmed);
  if (!Number.isInteger(parsed) || parsed <= 0) {
    return { error: "Validity must be a positive whole number of days." };
  }
  return {
    expiresAt: new Date(Date.now() + parsed * 24 * 60 * 60 * 1000).toISOString(),
  };
}

export default function DeveloperPage() {
  const router = useRouter();
  useSupabaseAuthSessionSync();
  const { ready: authSessionReady, session: authSession } = useAuthSessionState();

  const [sessionReady, setSessionReady] = useState(false);
  const [accessToken, setAccessToken] = useState("");
  const [snapshot, setSnapshot] = useState<UserSnapshot | null>(null);
  const [usageCount, setUsageCount] = useState(0);
  const [usageSummary, setUsageSummary] = useState<UsageSummary | null>(null);
  const [status, setStatus] = useState("Load account metrics for your signed-in account.");
  const [loading, setLoading] = useState(false);
  const [nickname, setNickname] = useState("default key");
  const [expiryDays, setExpiryDays] = useState("30");
  const [createdKey, setCreatedKey] = useState("");

  useEffect(() => {
    if (!authSessionReady) {
      return;
    }

    if (!authSession?.accessToken) {
      router.push("/auth");
      setSessionReady(true);
      return;
    }

    setAccessToken(authSession.accessToken);
    setSessionReady(true);
  }, [authSession, authSessionReady, router]);

  async function fetchSnapshot(options: { manageLoading?: boolean } = {}) {
    const { manageLoading = true } = options;

    if (!accessToken) {
      setStatus("Not authenticated.");
      return;
    }

    if (manageLoading) {
      setLoading(true);
    }

    try {
      const apiBase = getApiBase();
      const [meRes, usageRes] = await Promise.all([
        fetch(`${apiBase}/v1/users/me`, { headers: apiHeaders(accessToken) }),
        fetch(`${apiBase}/v1/usage`, { headers: apiHeaders(accessToken) }),
      ]);
      const meJson = (await meRes.json().catch(() => ({}))) as UserSnapshot & { error?: string };
      const usageJson = (await usageRes.json().catch(() => ({}))) as {
        data?: unknown[];
        summary?: UsageSummary;
        error?: string;
      };

      if (!meRes.ok) {
        setStatus(meJson.error ?? "Failed to load account data");
        return;
      }

      setSnapshot(meJson);
      if (!usageRes.ok) {
        setUsageCount(0);
        setUsageSummary(null);
        setStatus(`Loaded account data, but failed to load usage: ${usageJson.error ?? "Unknown usage error"}`);
        return;
      }

      setUsageCount(Array.isArray(usageJson.data) ? usageJson.data.length : 0);
      setUsageSummary(usageJson.summary ?? null);
      setStatus("Loaded developer account snapshot and usage analytics");
    } catch (error) {
      setStatus(error instanceof Error ? error.message : "Failed to load account data");
    } finally {
      if (manageLoading) {
        setLoading(false);
      }
    }
  }

  async function createExtraKey() {
    if (!accessToken) {
      setStatus("Not authenticated.");
      return;
    }

    setLoading(true);
    try {
      const apiBase = getApiBase();
      const { expiresAt, error } = toExpiry(expiryDays);
      if (error) {
        setStatus(error);
        return;
      }
      const response = await fetch(`${apiBase}/v1/users/api-keys`, {
        method: "POST",
        headers: apiHeaders(accessToken),
        body: JSON.stringify({
          nickname,
          scopes: [...DEFAULT_SCOPES],
          ...(expiresAt ? { expiresAt } : {}),
        }),
      });
      const json = await response.json();
      if (!response.ok) {
        setStatus(json.error ?? "Could not create API key");
        return;
      }

      setCreatedKey(typeof json.key === "string" ? json.key : "");
      setStatus("Generated additional API key");
      await fetchSnapshot({ manageLoading: false });
    } catch (error) {
      setStatus(error instanceof Error ? error.message : "Could not create API key");
    } finally {
      setLoading(false);
    }
  }

  async function revokeKey(id: string) {
    if (!accessToken) {
      setStatus("Not authenticated.");
      return;
    }

    setLoading(true);
    try {
      const apiBase = getApiBase();
      const response = await fetch(`${apiBase}/v1/users/api-keys/${id}/revoke`, {
        method: "POST",
        headers: apiHeaders(accessToken),
      });
      const json = await response.json().catch(() => ({}));
      if (!response.ok) {
        setStatus((json as { error?: string }).error ?? "Could not revoke API key");
        return;
      }
      setStatus("Revoked API key");
      await fetchSnapshot({ manageLoading: false });
    } catch (error) {
      setStatus(error instanceof Error ? error.message : "Could not revoke API key");
    } finally {
      setLoading(false);
    }
  }

  if (!sessionReady || !accessToken) {
    return null;
  }

  return (
    <DeveloperShell loading={loading} status={status}>
      <Card>
        <CardHeader>
          <CardTitle>API key controls</CardTitle>
          <CardDescription>Use a valid API key to inspect usage and generate developer-scoped keys.</CardDescription>
        </CardHeader>
        <CardContent className="space-y-4">
          <p className="text-sm text-muted-foreground">
            Authenticated via Supabase session. API calls use your session token. Raw keys are shown only once at creation.
          </p>
          <div className="grid gap-3 md:grid-cols-2">
            <label className="space-y-1 text-sm">
              <span className="text-muted-foreground">Nickname</span>
              <input
                className="w-full rounded-md border border-border bg-background px-3 py-2"
                value={nickname}
                onChange={(event) => setNickname(event.target.value)}
                placeholder="deploy key"
              />
            </label>
            <label className="space-y-1 text-sm">
              <span className="text-muted-foreground">Valid for days</span>
              <input
                className="w-full rounded-md border border-border bg-background px-3 py-2"
                value={expiryDays}
                onChange={(event) => setExpiryDays(event.target.value)}
                inputMode="numeric"
                placeholder="30"
              />
            </label>
          </div>
          <div className="flex flex-wrap gap-2">
            <Button disabled={loading} onClick={() => void fetchSnapshot()} type="button">
              Load usage
            </Button>
            <Button disabled={loading} onClick={() => void createExtraKey()} type="button" variant="secondary">
              Create API key
            </Button>
          </div>
          {createdKey ? (
            <div className="rounded-md border border-emerald-500/30 bg-emerald-500/10 p-3 text-sm">
              <p className="font-medium">New API key</p>
              <p className="break-all font-mono text-xs">{createdKey}</p>
            </div>
          ) : null}
        </CardContent>
      </Card>
      <UsageCards snapshot={snapshot} usageCount={usageCount} usageSummary={usageSummary} />
      <Card>
        <CardHeader>
          <CardTitle>Managed API keys</CardTitle>
          <CardDescription>Track nickname, expiry, status, and revoke keys without re-entering the secret value.</CardDescription>
        </CardHeader>
        <CardContent className="space-y-3">
          {!snapshot?.api_keys.length ? (
            <p className="text-sm text-muted-foreground">Load account data to inspect API keys.</p>
          ) : (
            snapshot.api_keys.map((key) => (
              <div key={key.id} className="rounded-md border border-border p-3">
                <div className="flex flex-wrap items-start justify-between gap-3">
                  <div className="space-y-1">
                    <p className="font-medium">{key.nickname}</p>
                    <p className="font-mono text-xs text-muted-foreground">{key.key_id}</p>
                    <p className="text-xs text-muted-foreground">{key.scopes.join(", ")}</p>
                  </div>
                  <div className="flex items-center gap-2">
                    <span className="rounded-full border border-border px-2 py-1 text-xs uppercase tracking-wide">
                      {key.status}
                    </span>
                    {key.status === "active" ? (
                      <Button disabled={loading} onClick={() => void revokeKey(key.id)} type="button" variant="outline">
                        Revoke
                      </Button>
                    ) : null}
                  </div>
                </div>
                <div className="mt-3 grid gap-1 text-xs text-muted-foreground sm:grid-cols-3">
                  <span>Created {new Date(key.createdAt).toLocaleString()}</span>
                  <span>{key.expiresAt ? `Expires ${new Date(key.expiresAt).toLocaleString()}` : "No expiry"}</span>
                  <span>{key.revokedAt ? `Revoked ${new Date(key.revokedAt).toLocaleString()}` : "Not revoked"}</span>
                </div>
              </div>
            ))
          )}
        </CardContent>
      </Card>
      <Card>
        <CardHeader>
          <CardTitle>Lifecycle activity</CardTitle>
          <CardDescription>Recent API key creation, revocation, and expiry observations.</CardDescription>
        </CardHeader>
        <CardContent className="space-y-3">
          {!snapshot?.api_key_events?.length ? (
            <p className="text-sm text-muted-foreground">No API key events loaded yet.</p>
          ) : (
            snapshot.api_key_events.map((event) => (
              <div key={event.id} className="rounded-md border border-border p-3 text-sm">
                <div className="flex flex-wrap items-center justify-between gap-2">
                  <span className="font-medium">{event.eventType}</span>
                  <span className="text-xs text-muted-foreground">{new Date(event.eventAt).toLocaleString()}</span>
                </div>
                <p className="mt-1 text-xs text-muted-foreground">Key {event.apiKeyId}</p>
              </div>
            ))
          )}
        </CardContent>
      </Card>
    </DeveloperShell>
  );
}
