import { cookies } from "next/headers";
import { redirect } from "next/navigation";

import { AuthShell } from "@/components/app-shell/auth-shell";
import { ConsentPanel } from "@/components/oauth/consent-panel";
import {
  decideConsentLanding,
  lookupGoTrueAuthorization,
} from "@/lib/auth/silent-consent";
import { createClient } from "@/lib/supabase/server";

interface ConsentPageProps {
  searchParams: Promise<{ authorization_id?: string }>;
}

/**
 * SSO wave 1: silent server-completed consent landing (spec 2026-08-23).
 *
 * With a valid console session this Server Component asks GoTrue whether the
 * authorization request is already covered by an active consent. When it is
 * (auto-approve), the response carries redirect_url and the page answers with
 * a bare server redirect: zero paint, zero client JS, at most one redirect
 * beyond the authorize round trip. Only when there is no session yet, when
 * GoTrue says consent is genuinely required, or when GoTrue is unreachable
 * does the interactive Approve/Deny panel render exactly as before wave 1.
 */
export default async function ConsentPage({ searchParams }: ConsentPageProps) {
  const { authorization_id: authorizationId } = await searchParams;

  const cookieStore = await cookies();
  const supabase = createClient(cookieStore);
  const { data } = await supabase.auth.getSession();
  const accessToken = data.session?.access_token ?? null;

  const lookup =
    accessToken && authorizationId && process.env.NEXT_PUBLIC_SUPABASE_URL
      ? await lookupGoTrueAuthorization(authorizationId, accessToken, {
          baseUrl: `${process.env.NEXT_PUBLIC_SUPABASE_URL}/auth/v1`,
          anonKey: process.env.NEXT_PUBLIC_SUPABASE_ANON_KEY ?? "",
        })
      : null;

  const decision = decideConsentLanding({
    hasSession: Boolean(accessToken),
    authorizationId: authorizationId ?? null,
    lookup,
  });

  if (decision.action === "silent-redirect") {
    // The one permitted hop out of this page: GoTrue's own auto-approve
    // redirect_url, already guarded against pointing back at this landing.
    redirect(decision.url);
  }
  if (decision.action === "sign-in") {
    redirect(decision.url);
  }

  if (decision.action === "error") {
    return (
      <AuthShell eyebrow="Sign-in request" title="Can't continue">
        <p role="alert" className="text-sm text-[var(--color-danger)]">
          {decision.message}
        </p>
      </AuthShell>
    );
  }

  return <ConsentPanel authorizationId={authorizationId ?? null} />;
}
