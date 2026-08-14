import Link from "next/link";
import { cookies } from "next/headers";
import { redirect } from "next/navigation";

import { createClient } from "@/lib/supabase/server";
import { appendNextParam } from "@/lib/auth/next-target";
import { AuthShell } from "@/components/app-shell/auth-shell";
import { Button, buttonVariants } from "@/components/ui/button";

interface AcceptPageProps {
  searchParams: Promise<{ token?: string }>;
}

interface InvitationErrorBody {
  error?: string;
  code?: string;
}

interface InvitationFailure {
  code: string | null;
  message: string | null;
}

function readErrorBody(text: string): InvitationFailure {
  if (!text) return { code: null, message: null };

  try {
    const payload: unknown = JSON.parse(text);
    if (payload === null || typeof payload !== "object") {
      return { code: null, message: null };
    }
    const candidate = payload as InvitationErrorBody;
    return {
      code: typeof candidate.code === "string" ? candidate.code : null,
      message: typeof candidate.error === "string" ? candidate.error : null,
    };
  } catch {
    return { code: null, message: null };
  }
}

export default async function AcceptInvitationPage({
  searchParams,
}: AcceptPageProps) {
  // Read the token before the auth check: a signed-out invitee has to carry it
  // through sign-in (or sign-up plus email confirmation) and back here, or the
  // invitation is unusable for exactly the people invitations are for. The
  // `next` value stays a relative, allow-listed path, so this does not widen
  // the open-redirect surface (see lib/auth/next-target.ts) (issue #534).
  const { token } = await searchParams;
  const returnTarget = token
    ? `/invitations/accept?token=${encodeURIComponent(token)}`
    : "/invitations/accept";

  const cookieStore = await cookies();
  const supabase = createClient(cookieStore);

  // Validate the JWT against Supabase (getUser round-trips and rejects
  // revoked tokens) before trusting the session for the bearer token.
  const {
    data: { user },
  } = await supabase.auth.getUser();

  if (!user) {
    redirect(appendNextParam("/auth/sign-in", returnTarget));
  }

  const {
    data: { session },
  } = await supabase.auth.getSession();

  if (!session) {
    redirect(appendNextParam("/auth/sign-in", returnTarget));
  }

  if (!token) {
    return (
      <AuthShell
        eyebrow="Invitation"
        title="Invalid invitation"
        subtitle="This link carried no invitation token. Ask the workspace owner to send a new invitation."
      >
        <Link
          href="/console"
          className={buttonVariants({ variant: "primary", size: "lg" })}
        >
          Go to console
        </Link>
      </AuthShell>
    );
  }

  const baseUrl = process.env.CONTROL_PLANE_BASE_URL;

  let failure: InvitationFailure = { code: null, message: null };
  let accepted = false;

  if (baseUrl) {
    try {
      const response = await fetch(`${baseUrl}/api/v1/invitations/accept`, {
        method: "POST",
        headers: {
          Authorization: `Bearer ${session.access_token}`,
          "Content-Type": "application/json",
        },
        body: JSON.stringify({ token }),
        cache: "no-store",
      });

      if (response.ok) {
        accepted = true;
      } else {
        failure = readErrorBody(await response.text().catch((): string => ""));
        if (!failure.message) {
          failure = {
            code: failure.code,
            message: `Failed to accept invitation (${response.status})`,
          };
        }
      }
    } catch {
      failure = {
        code: "network_error",
        message:
          "We could not reach the server. Check your connection and open the invitation link again.",
      };
    }
  } else {
    failure = {
      code: "server_configuration",
      message: "Server configuration error. Please contact support.",
    };
  }

  if (accepted) {
    // Redirect without changing hive_account_id — the newly joined workspace
    // appears in the switcher until the user explicitly selects it.
    redirect("/console/members?joined=1");
  }

  return renderFailure(failure, user.email ?? "");
}

// renderFailure states the real reason acceptance failed and offers the action
// that can actually resolve it. Telling every invitee to request a fresh link is
// wrong for a used token (a new link is not needed) and for the wrong signed-in
// account (a new link fails identically) (issue #534).
function renderFailure(failure: InvitationFailure, signedInEmail: string) {
  switch (failure.code) {
    case "invitation_expired":
      return (
        <AuthShell
          eyebrow="Invitation"
          title="This invitation has expired"
          subtitle="Invitations are valid for 72 hours. Ask the workspace owner to send a new invitation, then open the newest link."
        >
          <Link
            href="/console"
            className={buttonVariants({ variant: "primary", size: "lg" })}
          >
            Go to console
          </Link>
        </AuthShell>
      );

    case "invitation_already_accepted":
      return (
        <AuthShell
          eyebrow="Invitation"
          title="This invitation has already been accepted"
          subtitle="Nothing more to do. The workspace is available from the workspace switcher in the console sidebar."
        >
          <Link
            href="/console"
            className={buttonVariants({ variant: "primary", size: "lg" })}
          >
            Go to console
          </Link>
        </AuthShell>
      );

    case "invitation_already_member":
      return (
        <AuthShell
          eyebrow="Invitation"
          title="You are already in this workspace"
          subtitle="This invitation is for a workspace you already belong to. Pick it from the workspace switcher in the console sidebar."
        >
          <Link
            href="/console"
            className={buttonVariants({ variant: "primary", size: "lg" })}
          >
            Go to console
          </Link>
        </AuthShell>
      );

    // The one failure in this switch the invitee can clear themselves. The
    // control plane refuses acceptance until the address is confirmed, because
    // accepting is what grants the role, so the card names the action rather
    // than leaving them on a generic error with a link back to the console.
    case "invitation_email_not_verified":
      return (
        <AuthShell
          eyebrow="Invitation"
          title="Confirm your email address first"
          subtitle={
            <>
              Joining a workspace grants you a role in it, so the address on
              this account
              {signedInEmail ? (
                <>
                  {" ("}
                  <span className="font-mono text-[var(--color-ink)]">
                    {signedInEmail}
                  </span>
                  {")"}
                </>
              ) : null}{" "}
              has to be confirmed first. Open the confirmation email we sent
              when you signed up, then open this invitation link again. The
              invitation is still valid.
            </>
          }
        >
          <Link
            href="/console"
            className={buttonVariants({ variant: "primary", size: "lg" })}
          >
            Go to console
          </Link>
        </AuthShell>
      );

    case "invitation_email_mismatch":
      return (
        <AuthShell
          eyebrow="Invitation"
          title="Signed in as the wrong account"
          subtitle={
            <>
              This invitation was sent to a different email address than the one
              you are signed in with
              {signedInEmail ? (
                <>
                  {" ("}
                  <span className="font-mono text-[var(--color-ink)]">
                    {signedInEmail}
                  </span>
                  {")"}
                </>
              ) : null}
              . Sign out, sign in with the invited address, then open the
              invitation link again.
            </>
          }
        >
          {/* Sign-out is POST-only so SameSite=Lax cookies cannot be used to
              terminate a session through a cross-site navigation. */}
          <form method="POST" action="/auth/sign-out">
            <Button type="submit" variant="primary" size="lg">
              Sign out
            </Button>
          </form>
        </AuthShell>
      );

    case "invitation_not_found":
      return (
        <AuthShell
          eyebrow="Invitation"
          title="This invitation link is not valid"
          subtitle="The link may have been altered or the invitation may have been withdrawn. Ask the workspace owner to send a new invitation."
        >
          <Link
            href="/console"
            className={buttonVariants({ variant: "primary", size: "lg" })}
          >
            Go to console
          </Link>
        </AuthShell>
      );

    default:
      return (
        <AuthShell
          eyebrow="Invitation"
          title="Invitation error"
          subtitle={failure.message ?? "We couldn't accept this invitation."}
        >
          <Link
            href="/console"
            className={buttonVariants({ variant: "primary", size: "lg" })}
          >
            Go to console
          </Link>
        </AuthShell>
      );
  }
}
