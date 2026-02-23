"use client";

import { useState } from "react";

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

  const activeKeys = snapshot ? snapshot.api_keys.filter((key) => !key.revoked).length : 0;

  return (
    <section style={{ maxWidth: 920 }}>
      <h1>Billing and Usage</h1>
      <p>Manage prepaid credits and keys for this demo account.</p>

      <label style={{ display: "grid", gap: 6, marginBottom: 12 }}>
        API key
        <input value={apiKey} onChange={(event) => setApiKey(event.target.value)} placeholder="sk_live_..." />
      </label>

      <div style={{ display: "flex", gap: 8, marginBottom: 14, flexWrap: "wrap" }}>
        <button type="button" onClick={fetchSnapshot} disabled={loading}>
          Load account
        </button>
        <button type="button" onClick={createExtraKey} disabled={loading}>
          Create API key
        </button>
      </div>

      <div style={{ border: "1px solid #e5e7eb", borderRadius: 10, padding: 12, marginBottom: 12 }}>
        <h2 style={{ marginTop: 0 }}>Prepaid top-up (demo)</h2>
        <div style={{ display: "flex", gap: 8, alignItems: "center" }}>
          <input type="number" min={1} value={topUpAmount} onChange={(event) => setTopUpAmount(Number(event.target.value) || 1)} />
          <button type="button" onClick={topUpDemo} disabled={loading}>
            Top up now
          </button>
        </div>
        <p style={{ marginBottom: 0 }}>
          Latest intent: <strong>{latestIntent || "N/A"}</strong>
        </p>
      </div>

      <div style={{ border: "1px solid #e5e7eb", borderRadius: 10, padding: 12 }}>
        <h2 style={{ marginTop: 0 }}>Snapshot</h2>
        {snapshot ? (
          <>
            <p>
              <strong>User:</strong> {snapshot.user.email} ({snapshot.user.user_id})
            </p>
            <p>
              <strong>Credits:</strong> {snapshot.credits.availableCredits} available, {snapshot.credits.purchasedCredits} purchased, {snapshot.credits.promoCredits} promo
            </p>
            <p>
              <strong>Usage events:</strong> {usageCount}
            </p>
            <p>
              <strong>Active API keys:</strong> {activeKeys}
            </p>
          </>
        ) : (
          <p>Load account to view balances and usage.</p>
        )}
      </div>

      <p style={{ marginTop: 12, color: "#0f766e" }}>{status}</p>
    </section>
  );
}
