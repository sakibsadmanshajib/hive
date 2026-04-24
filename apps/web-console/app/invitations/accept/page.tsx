import { redirect } from "next/navigation";
import { cookies } from "next/headers";
import { createClient } from "@/lib/supabase/server";

interface AcceptPageProps {
  searchParams: Promise<{ token?: string }>;
}

function readErrorMessage(text: string): string | null {
  if (!text) {
    return null;
  }

  try {
    const payload: { error?: string } = JSON.parse(text);
    return typeof payload.error === "string" ? payload.error : null;
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
      <div>
        <h1>Invalid invitation</h1>
        <p>No invitation token was provided.</p>
        <a href="/console">Go to console</a>
      </div>
    );
  }

  const baseUrl = process.env.CONTROL_PLANE_BASE_URL;

  let errorMessage: string | null = null;
  let accepted = false;

  if (baseUrl) {
    try {
      const response = await fetch(
        `${baseUrl}/api/v1/invitations/accept`,
        {
          method: "POST",
          headers: {
            Authorization: `Bearer ${session.access_token}`,
            "Content-Type": "application/json",
          },
          body: JSON.stringify({ token }),
          cache: "no-store",
        }
      );

      if (response.ok) {
        accepted = true;
      } else {
        const responseText = await response.text().catch(() => "");
        errorMessage =
          readErrorMessage(responseText) ??
          `Failed to accept invitation (${response.status})`;
      }
    } catch {
      errorMessage = "Failed to connect to the server. Please try again.";
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
    <div>
      <h1>Invitation error</h1>
      <p>{errorMessage}</p>
      <a href="/console">Go to console</a>
    </div>
  );
}
