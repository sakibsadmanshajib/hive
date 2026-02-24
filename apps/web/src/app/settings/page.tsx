"use client";

import { useEffect, useState } from "react";
import { useRouter } from "next/navigation";

import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "../../components/ui/card";
import { readAuthSession } from "../../features/auth/auth-session";
import { TopUpPanel } from "../../features/billing/components/topup-panel";
import { SettingsShell } from "../../features/settings/components/settings-shell";
import { UserSettingsPanel } from "../../features/settings/user-settings-panel";

const apiBase = process.env.NEXT_PUBLIC_API_BASE_URL ?? "http://127.0.0.1:8080";

export default function SettingsPage() {
  const router = useRouter();
  const authSession = readAuthSession();

  const [apiKey, setApiKey] = useState(authSession?.apiKey ?? "");
  const [topUpAmount, setTopUpAmount] = useState(50);
  const [latestIntent, setLatestIntent] = useState("");
  const [status, setStatus] = useState("Manage account and billing settings.");
  const [loading, setLoading] = useState(false);

  useEffect(() => {
    if (!authSession?.apiKey) {
      router.push("/auth");
      return;
    }

    setApiKey(authSession.apiKey);
  }, [authSession?.apiKey, router]);

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
    } finally {
      setLoading(false);
    }
  }

  if (!authSession?.apiKey) {
    return null;
  }

  return (
    <SettingsShell loading={loading} status={status}>
      <div className="grid gap-4 lg:grid-cols-[1fr_1fr]">
        <Card>
          <CardHeader>
            <CardTitle>Profile information</CardTitle>
            <CardDescription>Identity data for your current session.</CardDescription>
          </CardHeader>
          <CardContent className="space-y-2">
            <p className="text-sm">
              <span className="font-medium">Name:</span> {authSession.name ?? "Not set"}
            </p>
            <p className="text-sm">
              <span className="font-medium">Email:</span> {authSession.email}
            </p>
            <p className="text-sm text-muted-foreground">Update profile editing APIs to make these fields writable from UI.</p>
          </CardContent>
        </Card>
        <Card>
          <CardHeader>
            <CardTitle>Payment methods</CardTitle>
            <CardDescription>Billing providers available for this demo account.</CardDescription>
          </CardHeader>
          <CardContent className="space-y-2 text-sm text-muted-foreground">
            <p>Primary method: bKash demo intent flow</p>
            <p>Alternative providers can be enabled once backend adapters are active.</p>
          </CardContent>
        </Card>
      </div>
      <TopUpPanel
        latestIntent={latestIntent}
        loading={loading}
        onTopUp={topUpDemo}
        setTopUpAmount={setTopUpAmount}
        topUpAmount={topUpAmount}
      />
      <UserSettingsPanel apiKey={apiKey} />
    </SettingsShell>
  );
}
