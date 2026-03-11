import { useEffect } from "react";
import { createBrowserClient } from "@supabase/ssr";
import type { Session } from "@supabase/supabase-js";
import { replaceAuthSession } from "../features/auth/auth-session";

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
    const nextSession = mapSessionToAuthSession(data.session);
    if (nextSession) {
      hasObservedSupabaseSession = true;
      replaceAuthSession(nextSession);
    }
  });

  const {
    data: { subscription },
  } = supabase.auth.onAuthStateChange((event, session) => {
    const nextSession = mapSessionToAuthSession(session);
    if (nextSession) {
      hasObservedSupabaseSession = true;
      replaceAuthSession(nextSession);
      return;
    }

    if (event === "SIGNED_OUT" && hasObservedSupabaseSession) {
      hasObservedSupabaseSession = false;
      replaceAuthSession(null);
    }
  });

  authSyncCleanup = () => {
    subscription.unsubscribe();
    authSyncCleanup = null;
    authSyncInitialized = false;
    hasObservedSupabaseSession = false;
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
