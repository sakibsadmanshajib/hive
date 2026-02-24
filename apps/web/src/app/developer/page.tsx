"use client";

import { useEffect, useState } from "react";
import { useRouter } from "next/navigation";

import { Button } from "../../components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "../../components/ui/card";
import { Input } from "../../components/ui/input";
import { readAuthSession } from "../../features/auth/auth-session";
import { UsageCards } from "../../features/billing/components/usage-cards";
import { DeveloperShell } from "../../features/developer/components/developer-shell";

const apiBase = process.env.NEXT_PUBLIC_API_BASE_URL ?? "http://127.0.0.1:8080";

type UserSnapshot = {
  user: { user_id: string; email: string; name?: string };
  credits: { availableCredits: number; purchasedCredits: number; promoCredits: number };
  api_keys: Array<{ key_id: string; revoked: boolean; scopes: string[]; createdAt: string }>;
};

export default function DeveloperPage() {
  const router = useRouter();
  const authSession = readAuthSession();

  const [apiKey, setApiKey] = useState(authSession?.apiKey ?? "");
  const [snapshot, setSnapshot] = useState<UserSnapshot | null>(null);
  const [usageCount, setUsageCount] = useState(0);
  const [status, setStatus] = useState("Load account metrics with your API key.");
  const [loading, setLoading] = useState(false);

  useEffect(() => {
    if (!authSession?.apiKey) {
      router.push("/auth");
    }
  }, [authSession?.apiKey, router]);

  async function fetchSnapshot() {
    if (!apiKey) {
      setStatus("Set API key first.");
      return;
    }

    setLoading(true);
    try {
      const [meRes, usageRes] = await Promise.all([
        fetch(`${apiBase}/v1/users/me`, { headers: { "x-api-key": apiKey } }),
        fetch(`${apiBase}/v1/usage`, { headers: { "x-api-key": apiKey } }),
      ]);
      const meJson = await meRes.json();
      const usageJson = await usageRes.json();
      if (!meRes.ok) {
        setStatus(meJson.error ?? "Failed to load account data");
        return;
      }

      setSnapshot(meJson);
      setUsageCount(Array.isArray(usageJson?.data) ? usageJson.data.length : 0);
      setStatus("Loaded developer account snapshot");
    } finally {
      setLoading(false);
    }
  }

  async function createExtraKey() {
    if (!apiKey) {
      setStatus("Set API key first.");
      return;
    }

    setLoading(true);
    try {
      const response = await fetch(`${apiBase}/v1/users/api-keys`, {
        method: "POST",
        headers: {
          "content-type": "application/json",
          "x-api-key": apiKey,
        },
        body: JSON.stringify({ scopes: ["chat", "image", "usage", "billing"] }),
      });
      const json = await response.json();
      if (!response.ok) {
        setStatus(json.error ?? "Could not create API key");
        return;
      }

      setStatus("Generated additional API key");
      await fetchSnapshot();
    } finally {
      setLoading(false);
    }
  }

  if (!authSession?.apiKey) {
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
          <label className="grid gap-2" htmlFor="developer-api-key">
            <span className="text-sm font-medium">Primary API key</span>
            <Input
              id="developer-api-key"
              onChange={(event) => setApiKey(event.target.value)}
              placeholder="sk_live_..."
              value={apiKey}
            />
          </label>
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
