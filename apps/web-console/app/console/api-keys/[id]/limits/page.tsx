import type { ReactElement } from "react";
import Link from "next/link";
import { redirect, notFound } from "next/navigation";
import { ArrowLeft } from "lucide-react";
import {
  ControlPlaneError,
  EMPTY_ACCOUNT_PROFILE,
  getAccountProfile,
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
import { ConsoleShell } from "@/components/app-shell/console-shell";
import { PageHeader } from "@/components/ui/page-header";

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

  // The shell needs a display name, and a profile fetch failure is not a reason
  // to fail the page: the viewer already carries everything the rail needs, and
  // the shell falls back to the email. This follows budget/page.tsx rather than
  // feature-gates/page.tsx: the canonical EMPTY_ACCOUNT_PROFILE sentinel rather
  // than an ad hoc object, and a log line, so a real control-plane outage on
  // this path leaves a trace instead of degrading silently.
  const profile = await getAccountProfile().catch((error: unknown) => {
    console.error("ApiKeyLimitsPage: could not load account profile", error);
    return EMPTY_ACCOUNT_PROFILE;
  });

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

  // Issue #543: this page rendered a bare <main> with no ConsoleShell, so it
  // had no navigation, no wordmark, no workspace switcher and no way back to
  // the key list except the browser's Back button. Combined with having no
  // inbound link at all, the only way in was to type a URL and the only way out
  // was to leave. active="/console/api-keys" keeps the rail lit on the section
  // this page belongs to, matching how the billing sub-pages point at
  // /console/billing.
  return (
    <ConsoleShell
      workspace={{
        id: viewer.current_account.id,
        name: viewer.current_account.display_name,
        slug: viewer.current_account.slug,
      }}
      memberships={viewer.memberships}
      viewer={viewer}
      user={{ email: viewer.user.email, name: profile.owner_name || null }}
      active="/console/api-keys"
      topbar={
        <span className="font-medium text-[var(--color-ink-2)]">
          Rate limits
        </span>
      }
    >
      <Link
        href="/console/api-keys"
        className="mb-4 inline-flex items-center gap-1.5 text-xs text-[var(--color-ink-3)] transition-colors hover:text-[var(--color-ink)]"
      >
        <ArrowLeft size={12} />
        All API keys
      </Link>
      <PageHeader
        eyebrow="Authentication"
        title="Rate limits"
        description="Per-key request and token limits. Tier overrides take precedence over system defaults for the matching tier."
      />
      <RateLimitForm initial={limits} canEdit={canEdit} onSave={saveLimits} />
    </ConsoleShell>
  );
}
