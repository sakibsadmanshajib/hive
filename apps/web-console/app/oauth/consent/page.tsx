import { cookies } from "next/headers";
import { redirect } from "next/navigation";

import { AuthShell } from "@/components/app-shell/auth-shell";
import { ConsentPanel } from "@/components/oauth/consent-panel";
import { CONSENT_RETRIED_PARAM } from "@/lib/auth/next-target";
import {
  decideConsentLanding,
  lookupGoTrueAuthorization,
} from "@/lib/auth/silent-consent";
import { createClient } from "@/lib/supabase/server";

interface ConsentPageProps {
  // Next hands a repeated query param through as an array, so the honest type
  // is the union. `?authorization_id=a&authorization_id=b` must not be quietly
  // collapsed into a fabricated single id: readSingle refuses the array and
  // the request falls to the panel, which paints its missing-id error.
  searchParams: Promise<Record<string, string | string[] | undefined>>;
}

function readSingle(value: string | string[] | undefined): string | null {
  return typeof value === "string" && value.length > 0 ? value : null;
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
  const params = await searchParams;
  const authorizationId = readSingle(params.authorization_id);
  // Presence of the key, not a parsed single value. `?retried=` with an empty
  // value, or a repeated `?retried=1&retried=1`, both make readSingle answer
  // null, and reading the marker through it would let either spelling disarm
  // the hop bound this marker exists to enforce.
  const signInAlreadyAttempted = CONSENT_RETRIED_PARAM in params;

  const cookieStore = await cookies();
  const supabase = createClient(cookieStore);
  const { data, error: sessionError } = await supabase.auth.getSession();
  const accessToken = data.session?.access_token ?? null;

  // A config gap (either Supabase var unset) is not an auth failure: skip the
  // LOOKUP only and let the panel handle the request, exactly as before this
  // change. The session read above is unconditional, as it is on every other
  // console page.
  const lookup =
    accessToken &&
    authorizationId &&
    process.env.NEXT_PUBLIC_SUPABASE_URL &&
    process.env.NEXT_PUBLIC_SUPABASE_ANON_KEY
      ? await lookupGoTrueAuthorization(authorizationId, accessToken, {
          baseUrl: `${process.env.NEXT_PUBLIC_SUPABASE_URL}/auth/v1`,
          anonKey: process.env.NEXT_PUBLIC_SUPABASE_ANON_KEY,
        })
      : null;

  const decision = decideConsentLanding({
    hasSession: Boolean(accessToken),
    // A failed read is not an absent session. supabase-js reports an expired
    // token whose refresh could not complete as {session: null, error}, so
    // without this the server would send a signed-in user to the password
    // form because GoTrue was briefly unreachable.
    sessionReadFailed: Boolean(sessionError),
    authorizationId,
    lookup,
    signInAlreadyAttempted,
  });

  // Both redirect() calls are terminal: next/navigation throws to unwind, so
  // neither may sit inside a try block and the branches below are unreachable
  // once one fires. They stay separate ifs rather than else-ifs because the
  // union is exhaustive and each branch names its own reason.
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

  return (
    <ConsentPanel
      authorizationId={authorizationId}
      signInAlreadyAttempted={signInAlreadyAttempted}
    />
  );
}
