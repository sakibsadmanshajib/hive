"use client";

import { useEffect, useRef } from "react";
import { useRouter } from "next/navigation";

import { createClient } from "@/lib/supabase/browser";

// Claims are minted when a token is issued, so a user whose tenant membership
// was just created still holds an access token with no tenant_id claim. This
// refreshes the session once to pick up the claim, then re-renders the route so
// the layout stops taking the no-tenant path on every navigation.
//
// Errors are swallowed on purpose. A failed refresh is self-correcting (the
// token refreshes on its own schedule, and the layout's reconcile call is
// idempotent), so it is not worth breaking the console over.
//
// The refresh cannot spin. router.refresh() re-fetches the server tree and
// reconciles it into the same component position, so the ref survives and the
// effect does not run again even if the new token still lacks the claim. The
// worst case is one refresh per hard navigation while that state persists,
// which is bounded rather than a loop.
export function TenantClaimRefresh() {
  const router = useRouter();
  // React strict mode mounts effects twice in development; without this guard
  // the session would be refreshed twice on first paint.
  const startedRef = useRef(false);

  useEffect(() => {
    if (startedRef.current) {
      return;
    }
    startedRef.current = true;

    const supabase = createClient();
    supabase.auth
      .refreshSession()
      .then(() => {
        router.refresh();
      })
      .catch(() => {
        // Intentionally ignored, see comment above.
      });
  }, [router]);

  return null;
}
