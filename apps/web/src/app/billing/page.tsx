"use client";

import { useState } from "react";

import { Button } from "../../components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "../../components/ui/card";
import { Input } from "../../components/ui/input";
import { BillingShell } from "../../features/billing/components/billing-shell";
import { TopUpPanel } from "../../features/billing/components/topup-panel";
import { UsageCards } from "../../features/billing/components/usage-cards";

const apiBase = process.env.NEXT_PUBLIC_API_BASE_URL ?? "http://127.0.0.1:8080";

type UserSnapshot = {
  user: { user_id: string; email: string; name?: string };
  credits: { availableCredits: number; purchasedCredits: number; promoCredits: number };
  api_keys: Array<{ key_id: string; revoked: boolean; scopes: string[]; createdAt: string }>;
};

export default function BillingPage() {
  const [apiKey, setApiKey] = useState("");
  const [snapshot, setSnapshot] = useState<UserSnapshot | null>(null);
  const [usageCount, setUsageCount] = useState(0);
  const [topUpAmount, setTopUpAmount] = useState(50);
  const [latestIntent, setLatestIntent] = useState("");
  const [status, setStatus] = useState("Idle");
  const [loading, setLoading] = useState(false);

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
      setStatus("Loaded account snapshot");
    } finally {
      setLoading(false);
    }
  }

  async function topUpDemo() {
    if (!apiKey) {
      setStatus("Set API key first.");
      return;
    }

    setLoading(true);
    try {
      const intentRes = await fetch(`${apiBase}/v1/payments/intents`, {
        method: "POST",
        headers: {
          "content-type": "application/json",
          "x-api-key": apiKey,
        },
        body: JSON.stringify({ bdt_amount: topUpAmount, provider: "bkash" }),
      });
      const intentJson = await intentRes.json();
      if (!intentRes.ok) {
        setStatus(intentJson.error ?? "Could not create payment intent");
        return;
      }

      const intentId = intentJson.intent_id;
      setLatestIntent(intentId);

      const confirmRes = await fetch(`${apiBase}/v1/payments/demo/confirm`, {
        method: "POST",
        headers: {
          "content-type": "application/json",
          "x-api-key": apiKey,
        },
        body: JSON.stringify({ intent_id: intentId }),
      });
      const confirmJson = await confirmRes.json();
      if (!confirmRes.ok) {
        setStatus(confirmJson.error ?? "Demo confirm failed");
        return;
      }

      setStatus(`Top-up successful (+${confirmJson.minted_credits} credits)`);
      await fetchSnapshot();
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

  return (
    <BillingShell loading={loading} status={status}>
      <div className="grid grid-cols-1 gap-4 lg:grid-cols-[1.2fr_1fr]">
        <Card>
          <CardHeader>
            <CardTitle>Account controls</CardTitle>
            <CardDescription>Use a valid API key to inspect account state and create secondary keys.</CardDescription>
          </CardHeader>
          <CardContent className="space-y-4">
            <label className="grid gap-2" htmlFor="billing-api-key">
              <span className="text-sm font-medium">Primary API key</span>
              <Input
                id="billing-api-key"
                onChange={(event) => setApiKey(event.target.value)}
                placeholder="sk_live_..."
                value={apiKey}
              />
            </label>
            <div className="flex flex-wrap gap-2">
              <Button disabled={loading} onClick={() => void fetchSnapshot()} type="button">
                Load account
              </Button>
              <Button disabled={loading} onClick={() => void createExtraKey()} type="button" variant="secondary">
                Create API key
              </Button>
            </div>
          </CardContent>
        </Card>
        <TopUpPanel
          latestIntent={latestIntent}
          loading={loading}
          onTopUp={topUpDemo}
          setTopUpAmount={setTopUpAmount}
          topUpAmount={topUpAmount}
        />
      </div>
      <UsageCards snapshot={snapshot} usageCount={usageCount} />
    </BillingShell>
  );
}
