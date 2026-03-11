"use client";

import { useEffect, useState } from "react";
import { useRouter } from "next/navigation";

import { Button } from "../../components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "../../components/ui/card";
import { useAuthSessionState } from "../../features/auth/auth-session";
import { UsageCards, type UserSnapshot } from "../../features/billing/components/usage-cards";
import { DeveloperShell } from "../../features/developer/components/developer-shell";
import { apiHeaders, getApiBase } from "../../lib/api";
import { useSupabaseAuthSessionSync } from "../../lib/supabase-client";

export default function DeveloperPage() {
  const router = useRouter();
  useSupabaseAuthSessionSync();
  const { ready: authSessionReady, session: authSession } = useAuthSessionState();

  const [sessionReady, setSessionReady] = useState(false);
  const [accessToken, setAccessToken] = useState("");
  const [snapshot, setSnapshot] = useState<UserSnapshot | null>(null);
  const [usageCount, setUsageCount] = useState(0);
  const [status, setStatus] = useState("Load account metrics for your signed-in account.");
  const [loading, setLoading] = useState(false);

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
      const usageJson = (await usageRes.json().catch(() => ({}))) as { data?: unknown[]; error?: string };

      if (!meRes.ok) {
        setStatus(meJson.error ?? "Failed to load account data");
        return;
      }

      setSnapshot(meJson);
      if (!usageRes.ok) {
        setUsageCount(0);
        setStatus(`Loaded account data, but failed to load usage: ${usageJson.error ?? "Unknown usage error"}`);
        return;
      }

      setUsageCount(Array.isArray(usageJson.data) ? usageJson.data.length : 0);
      setStatus("Loaded developer account snapshot");
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
      const response = await fetch(`${apiBase}/v1/users/api-keys`, {
        method: "POST",
        headers: apiHeaders(accessToken),
        body: JSON.stringify({ scopes: ["chat", "image", "usage", "billing"] }),
      });
      const json = await response.json();
      if (!response.ok) {
        setStatus(json.error ?? "Could not create API key");
        return;
      }

      setStatus("Generated additional API key");
      await fetchSnapshot({ manageLoading: false });
    } catch (error) {
      setStatus(error instanceof Error ? error.message : "Could not create API key");
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
            Authenticated via Supabase session. API calls use your session token.
          </p>
          <div className="flex flex-wrap gap-2">
            <Button disabled={loading} onClick={() => void fetchSnapshot()} type="button">
              Load usage
            </Button>
            <Button disabled={loading} onClick={() => void createExtraKey()} type="button" variant="secondary">
              Create API key
            </Button>
          </div>
        </CardContent>
      </Card>
      <UsageCards snapshot={snapshot} usageCount={usageCount} />
    </DeveloperShell>
  );
}
