import { useEffect, useState } from "react";

export type AuthSession = {
  accessToken: string;
  email: string;
  name?: string;
};

export const AUTH_STORAGE_KEY = "bdai.auth.session";

const listeners = new Set<() => void>();

let cachedSession: AuthSession | null | undefined;
let storageListenerAttached = false;

function readStoredAuthSession(): AuthSession | null {
  if (typeof window === "undefined") {
    return null;
  }

  const rawValue = window.localStorage.getItem(AUTH_STORAGE_KEY);
  if (!rawValue) {
    return null;
  }

  try {
    return JSON.parse(rawValue) as AuthSession;
  } catch {
    return null;
  }
}

function notifyAuthSessionListeners() {
  for (const listener of listeners) {
    listener();
  }
}

function setCachedSession(session: AuthSession | null) {
  cachedSession = session;
  notifyAuthSessionListeners();
}

function ensureStorageListener() {
  if (storageListenerAttached || typeof window === "undefined") {
    return;
  }

  window.addEventListener("storage", (event) => {
    if (event.key !== AUTH_STORAGE_KEY) {
      return;
    }

    cachedSession = readStoredAuthSession();
    notifyAuthSessionListeners();
  });

  storageListenerAttached = true;
}

export function readAuthSession(): AuthSession | null {
  if (cachedSession === undefined) {
    cachedSession = readStoredAuthSession();
  }

  return cachedSession;
}

export function writeAuthSession(session: AuthSession): void {
  if (typeof window === "undefined") {
    return;
  }

  window.localStorage.setItem(AUTH_STORAGE_KEY, JSON.stringify(session));
  setCachedSession(session);
}

export function clearAuthSession(): void {
  if (typeof window === "undefined") {
    return;
  }

  window.localStorage.removeItem(AUTH_STORAGE_KEY);
  setCachedSession(null);
}

export function replaceAuthSession(session: AuthSession | null): void {
  if (!session) {
    clearAuthSession();
    return;
  }

  writeAuthSession(session);
}

export function subscribeAuthSession(listener: () => void): () => void {
  ensureStorageListener();
  listeners.add(listener);

  return () => {
    listeners.delete(listener);
  };
}

export function useAuthSessionState(): { ready: boolean; session: AuthSession | null } {
  const [session, setSession] = useState<AuthSession | null>(null);
  const [ready, setReady] = useState(false);

  useEffect(() => {
    const syncSession = () => {
      setSession(readAuthSession());
      setReady(true);
    };

    syncSession();
    return subscribeAuthSession(syncSession);
  }, []);

  return { ready, session };
}

export function useAuthSession(): AuthSession | null {
  return useAuthSessionState().session;
}
