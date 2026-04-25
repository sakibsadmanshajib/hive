import Link from "next/link";
import { cookies } from "next/headers";
import { redirect } from "next/navigation";

import { createClient } from "@/lib/supabase/server";
import { AuthShell } from "@/components/app-shell/auth-shell";
import { buttonVariants } from "@/components/ui/button";

interface AcceptPageProps {
  searchParams: Promise<{ token?: string }>;
}

interface InvitationErrorBody {
  error?: string;
}

function readErrorMessage(text: string): string | null {
  if (!text) return null;

  try {
    const payload: unknown = JSON.parse(text);
    if (payload === null || typeof payload !== "object") return null;
    const candidate = payload as InvitationErrorBody;
    return typeof candidate.error === "string" ? candidate.error : null;
  } catch {
    return null;
  }
}

export default async function AcceptInvitationPage({
  searchParams,
}: AcceptPageProps) {
  const cookieStore = await cookies();
  const supabase = createClient(cookieStore);

  const {
    data: { session },
  } = await supabase.auth.getSession();

  if (!session) {
    redirect("/auth/sign-in?next=/invitations/accept");
  }

  const { token } = await searchParams;

  if (!token) {
    return (
      <AuthShell
        eyebrow="Invitation"
        title="Invalid invitation"
        subtitle="No invitation token was provided. Ask the workspace owner for a fresh link."
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

  let errorMessage: string | null = null;
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
        const responseText = await response.text().catch((): string => "");
        errorMessage =
          readErrorMessage(responseText) ??
          `Failed to accept invitation (${response.status})`;
      }
    } catch (err: unknown) {
      const message =
        err instanceof Error
          ? err.message
          : "Failed to connect to the server. Please try again.";
      errorMessage = message;
    }
  } else {
    errorMessage = "Server configuration error. Please contact support.";
  }

  if (accepted) {
    // Redirect without changing hive_account_id — the newly joined workspace
    // appears in the switcher until the user explicitly selects it.
    redirect("/console/members?joined=1");
  }

  return (
    <AuthShell
      eyebrow="Invitation"
      title="Invitation error"
      subtitle={errorMessage ?? "We couldn't accept this invitation."}
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
