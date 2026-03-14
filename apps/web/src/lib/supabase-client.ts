import { useEffect } from "react";
import { createBrowserClient } from "@supabase/ssr";
import type { Session } from "@supabase/supabase-js";
import { replaceAuthSession } from "../features/auth/auth-session";
import { clearGuestSession, readGuestSession } from "../features/auth/guest-session";
import { getAppUrl } from "./api";

function requirePublicEnv(name: string, value: string | undefined): string {
  if (!value) {
    throw new Error(`${name} is required`);
  }
  return value;
}

export function createSupabaseBrowserClient() {
  return createBrowserClient(
    requirePublicEnv("NEXT_PUBLIC_SUPABASE_URL", process.env.NEXT_PUBLIC_SUPABASE_URL),
    requirePublicEnv("NEXT_PUBLIC_SUPABASE_ANON_KEY", process.env.NEXT_PUBLIC_SUPABASE_ANON_KEY),
  );
}

function mapSessionToAuthSession(session: Session | null) {
  if (!session?.access_token || !session.user.email) {
    return null;
  }

  return {
    accessToken: session.access_token,
    email: session.user.email,
    name: typeof session.user.user_metadata?.name === "string" ? session.user.user_metadata.name : undefined,
  };
}

let authSyncInitialized = false;
let authSyncCleanup: (() => void) | null = null;
let authSyncConsumers = 0;
let hasObservedSupabaseSession = false;
let linkedGuestIdentity: string | null = null;

async function linkGuestSession(accessToken: string, userId?: string) {
  const guestSession = readGuestSession();
  if (!guestSession) {
    return;
  }

  const identityKey = `${guestSession.guestId}:${userId ?? "unknown"}`;
  if (linkedGuestIdentity === identityKey) {
    return;
  }

  try {
    const response = await fetch(getAppUrl("/api/guest-session/link"), {
      method: "POST",
      headers: {
        authorization: `Bearer ${accessToken}`,
      },
    });

    if (response.ok) {
      linkedGuestIdentity = identityKey;
      clearGuestSession();
    }
  } catch {
    // Keep the guest session so a later authenticated event can retry the link.
  }
}

export function ensureSupabaseAuthSessionSync(): () => void {
  if (typeof window === "undefined") {
    return () => undefined;
  }

  if (authSyncCleanup) {
    return authSyncCleanup;
  }

  const supabase = createSupabaseBrowserClient();
  authSyncInitialized = true;

  void supabase.auth.getSession().then(({ data }) => {
    const session = data.session;
    const nextSession = mapSessionToAuthSession(session);
    if (nextSession && session) {
      hasObservedSupabaseSession = true;
      replaceAuthSession(nextSession);
      void linkGuestSession(session.access_token, session.user.id);
    }
  });

  const {
    data: { subscription },
  } = supabase.auth.onAuthStateChange((event, session) => {
    const nextSession = mapSessionToAuthSession(session);
    if (nextSession && session) {
      hasObservedSupabaseSession = true;
      replaceAuthSession(nextSession);
      void linkGuestSession(session.access_token, session.user.id);
      return;
    }

    if (event === "SIGNED_OUT" && hasObservedSupabaseSession) {
      hasObservedSupabaseSession = false;
      linkedGuestIdentity = null;
      replaceAuthSession(null);
    }
  });

  authSyncCleanup = () => {
    subscription.unsubscribe();
    authSyncCleanup = null;
    authSyncInitialized = false;
    hasObservedSupabaseSession = false;
    linkedGuestIdentity = null;
  };

  return authSyncCleanup;
}

export function useSupabaseAuthSessionSync() {
  useEffect(() => {
    const cleanup = ensureSupabaseAuthSessionSync();
    authSyncConsumers += 1;

    return () => {
      authSyncConsumers -= 1;
      if (authSyncConsumers <= 0) {
        cleanup();
        authSyncConsumers = 0;
      }
    };
  }, []);
}
