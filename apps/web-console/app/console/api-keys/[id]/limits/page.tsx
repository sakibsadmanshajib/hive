import type { ReactElement } from "react";
import { redirect, notFound } from "next/navigation";
import { getViewer } from "@/lib/control-plane/client";
import {
  getKeyLimits,
  type ApiKeysClient,
  type KeyLimits,
} from "@/lib/api-keys";
import { RateLimitForm } from "@/components/api-keys/rate-limit-form";

interface PageProps {
  params: Promise<{ id: string }>;
}

// serverFetchClient is the server-side fetch wrapper for the control-plane.
// In the browser, the form uses window.fetch via the same ApiKeysClient
// interface; we intentionally narrow to that interface so the form is
// transport-agnostic and easily mocked in tests.
const serverFetchClient: ApiKeysClient = {
  fetch: (input, init) => fetch(input, init),
};

export default async function ApiKeyLimitsPage(props: PageProps): Promise<ReactElement> {
  const { id: keyID } = await props.params;
  const viewer = await getViewer();

  // Account-membership gate runs before the control-plane round-trip.
  // Authenticated users without an active account row should never reach
  // the limits page — bounce to profile setup. `current_account.id` is
  // the membership invariant; `email` would always be present for any
  // logged-in viewer and is the wrong signal here.
  if (!viewer.current_account?.id) {
    redirect("/console/settings/profile");
  }

  // Owner-gate: members without can_manage_api_keys see read-only.
  const canEdit = viewer.gates.can_manage_api_keys;

  let limits: KeyLimits;
  try {
    limits = await getKeyLimits(serverFetchClient, keyID);
  } catch (err) {
    // 404 surfaced from control-plane → next/navigation 404.
    if (err instanceof Error && err.message.includes("404")) {
      notFound();
    }
    throw err;
  }

  return (
    <main className="px-6 py-8">
      <h1 className="text-2xl font-semibold">Rate limits</h1>
      <p className="text-sm text-[var(--color-ink-2)] mb-4">
        Configure per-key request and token limits. Tier overrides take
        precedence over system defaults for the matching tier.
      </p>
      <RateLimitForm
        keyID={keyID}
        initial={limits}
        canEdit={canEdit}
        client={serverFetchClient}
      />
    </main>
  );
}
