import type { ReactElement } from "react";
import { redirect, notFound } from "next/navigation";
import {
  ControlPlaneError,
  getApiKeyLimits,
  getViewer,
  updateApiKeyLimits,
} from "@/lib/control-plane/client";
import { can } from "@/lib/viewer-gates";
import {
  parseKeyLimitsInput,
  type KeyLimits,
  type SaveLimitsResult,
} from "@/lib/api-keys";
import { RateLimitForm } from "@/components/api-keys/rate-limit-form";

interface PageProps {
  params: Promise<{ id: string }>;
}

// Upstream refusals are reported in this app's own words. The upstream text can
// name internal state, so only the status and the stable machine code are read.
function saveErrorMessage(err: unknown): string {
  if (err instanceof ControlPlaneError) {
    if (err.status === 403) return "You do not have permission to change rate limits.";
    if (err.status === 404) return "This key no longer exists.";
    if (err.status === 422) return "One of these rate-limit values is out of range.";
    if (err.status === 409) return "These limits were changed elsewhere. Reload and try again.";
  }
  return "Could not save the rate limits. Please try again.";
}

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

  // Owner-gate: members without api_keys.write see read-only.
  const canEdit = can(viewer, "api_keys.write");

  let limits: KeyLimits;
  try {
    limits = await getApiKeyLimits(keyID);
  } catch (err) {
    // A key that is not this account's own reads as 404 upstream. Branch on the
    // status rather than on message text: the previous message match also let a
    // transport failure through as an uncaught error and crashed the page.
    if (err instanceof ControlPlaneError && err.status === 404) {
      notFound();
    }
    throw err;
  }

  // The write runs as a server action, so the browser form never needs the
  // control-plane origin or a fetch client passed down from here (a function is
  // not a serialisable prop for a Client Component in the first place).
  async function saveLimits(input: unknown): Promise<SaveLimitsResult> {
    "use server";

    // A server action is a public endpoint: the disabled fieldset is
    // presentation only, so permission is resolved again from the caller's own
    // session, and the payload is parsed rather than trusted.
    const actor = await getViewer();
    if (!can(actor, "api_keys.write")) {
      return { ok: false, error: "You do not have permission to change rate limits." };
    }

    const parsed = parseKeyLimitsInput(input);
    if (parsed === null) {
      return { ok: false, error: "These rate-limit values are not valid." };
    }

    try {
      await updateApiKeyLimits(keyID, parsed);
      return { ok: true };
    } catch (err) {
      return { ok: false, error: saveErrorMessage(err) };
    }
  }

  return (
    <main className="px-6 py-8">
      <h1 className="text-2xl font-semibold">Rate limits</h1>
      <p className="text-sm text-[var(--color-ink-2)] mb-4">
        Configure per-key request and token limits. Tier overrides take
        precedence over system defaults for the matching tier.
      </p>
      <RateLimitForm initial={limits} canEdit={canEdit} onSave={saveLimits} />
    </main>
  );
}
