"use client";

import { useEffect, useState } from "react";
import { useRouter } from "next/navigation";

import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "../../components/ui/card";
import { readAuthSession } from "../../features/auth/auth-session";
import { TopUpPanel } from "../../features/billing/components/topup-panel";
import { SettingsShell } from "../../features/settings/components/settings-shell";
import { UserSettingsPanel } from "../../features/settings/user-settings-panel";
import { apiBase } from "../../lib/api-base";

type ProfileSession = {
  email: string;
  name?: string;
};

export default function SettingsPage() {
  const router = useRouter();

  const [sessionReady, setSessionReady] = useState(false);
  const [profile, setProfile] = useState<ProfileSession | null>(null);
  const [apiKey, setApiKey] = useState("");
  const [topUpAmount, setTopUpAmount] = useState(50);
  const [latestIntent, setLatestIntent] = useState("");
  const [status, setStatus] = useState("Manage account and billing settings.");
  const [loading, setLoading] = useState(false);

  useEffect(() => {
    const authSession = readAuthSession();
    if (!authSession?.apiKey) {
      router.push("/auth");
      setSessionReady(true);
      return;
    }

    setProfile({ email: authSession.email, name: authSession.name });
    setApiKey(authSession.apiKey);
    setSessionReady(true);
  }, [router]);

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
      const intentJson = (await intentRes.json().catch(() => ({}))) as { error?: string; intent_id?: string };
      if (!intentRes.ok) {
        setStatus(intentJson.error ?? "Could not create payment intent");
        return;
      }

      const intentId = intentJson.intent_id;
      if (!intentId) {
        setStatus("Could not create payment intent");
        return;
      }

      setLatestIntent(intentId);

      const confirmRes = await fetch(`${apiBase}/v1/payments/demo/confirm`, {
        method: "POST",
        headers: {
          "content-type": "application/json",
          "x-api-key": apiKey,
        },
        body: JSON.stringify({ intent_id: intentId }),
      });
      const confirmJson = (await confirmRes.json().catch(() => ({}))) as { error?: string; minted_credits?: number };
      if (!confirmRes.ok) {
        setStatus(confirmJson.error ?? "Demo confirm failed");
        return;
      }

      setStatus(`Top-up successful (+${confirmJson.minted_credits} credits)`);
    } catch (error) {
      setStatus(error instanceof Error ? error.message : "Unexpected error during top-up");
    } finally {
      setLoading(false);
    }
  }

  if (!sessionReady || !apiKey || !profile) {
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
              <span className="font-medium">Name:</span> {profile.name ?? "Not set"}
            </p>
            <p className="text-sm">
              <span className="font-medium">Email:</span> {profile.email}
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
