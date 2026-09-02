import type { ReactNode } from "react";
import { cookies } from "next/headers";
import { redirect } from "next/navigation";

import { getBalance, getBudgetThreshold } from "@/lib/control-plane/client";
import { requireViewer, tolerate } from "@/lib/console/data";
import { readTenantIdClaim } from "@/lib/auth/tenant-claim";
import { createClient } from "@/lib/supabase/server";
import { VerificationBanner } from "@/components/verification-banner";
import { BudgetAlertBanner } from "@/components/billing/budget-alert-banner";

interface ConsoleLayoutProps {
  children: ReactNode;
}

// Layout-level concern: the workspace verification + budget banners. The
// page-level <ConsoleShell/> takes care of sidebar + topbar + content
// composition (including the visible workspace switcher, see
// components/workspace-switcher.tsx) so each page picks its own `active`
// route.
export default async function ConsoleLayout({ children }: ConsoleLayoutProps) {
  // requireViewer() carries the redirect-to-sign-in fallback that used to be
  // written out here, so every console route now shares it rather than this
  // layout being the only place that had it (issue #494). It also memoizes
  // the read for the request, so the page beneath this layout reuses this
  // answer instead of making a second, independently failable viewer call.
  const viewer = await requireViewer();

  // A signed-in user can legitimately hold no tenant membership: the Supabase
  // access-token hook issues a valid token with no tenant_id claim rather than
  // failing sign-in. Billing accounts are auto-provisioned on first login, so
  // requireViewer() above still succeeds; only the tenant scope can be missing.
  //
  // Hand that case to /console/provision rather than settling it here. This is
  // a layout, so it re-renders on every navigation, and a provisioning write
  // must not sit on a render path. The handler also needs to write cookies to
  // refresh the session, which a Server Component cannot do. Reading the claim
  // is free, so a tenanted user takes exactly the path they took before with no
  // extra request.
  const cookieStore = await cookies();
  const {
    data: { session },
  } = await createClient(cookieStore).auth.getSession();
  if (!readTenantIdClaim(session?.access_token)) {
    redirect("/console/provision");
  }

  const isUnverified = viewer.user.email_verified === false;

  // A balance that failed to load is unknown, and unknown is not zero. This
  // read used to collapse a rejection to 0, which is below every threshold a
  // customer can set, so an unreadable balance rendered "your balance has
  // reached or dropped below your alert threshold" on every console route at
  // once (issue #494). Pass the uncertainty through instead and let the
  // banner stay quiet; a warning nobody can act on is worse than no warning.
  const [currentBalance, threshold] = isUnverified
    ? [null, null]
    : await Promise.all([
        tolerate(getBalance()).then(
          (balance) => balance?.available_credits ?? null,
        ),
        tolerate(getBudgetThreshold()),
      ]);

  return (
    <>
      <VerificationBanner show={isUnverified} />
      {!isUnverified && (
        <BudgetAlertBanner
          threshold={threshold}
          currentBalance={currentBalance}
        />
      )}
      {children}
    </>
  );
}
