import { redirect } from "next/navigation";
import { cookies } from "next/headers";

import { NoWorkspaceState } from "@/components/console/no-workspace-state";
import { createClient } from "@/lib/supabase/server";

// Terminal state for a signed-in user whom no workspace claims.
//
// Deliberately outside /console. The console layout redirects a tenant-less
// viewer into /console/provision, so a page rendered under that layout would be
// bounced straight back into the handler on every visit. Sitting at the top
// level makes this a stable place to land.
//
// Authentication is enforced here rather than inherited, because middleware only
// gates /console. There is nothing sensitive on the page beyond the viewer's own
// email address, but an unauthenticated visitor has no business reading a state
// that describes somebody's account, and without the check the email lookup
// below would simply fail.
export default async function NoWorkspacePage() {
  const cookieStore = await cookies();
  const {
    data: { user },
  } = await createClient(cookieStore).auth.getUser();

  if (!user) {
    redirect("/auth/sign-in");
  }

  return <NoWorkspaceState email={user.email ?? ""} />;
}
