import { useSyncExternalStore } from "react";

export type GuestSession = {
  guestId: string;
  issuedAt: string;
  expiresAt: string;
  /** Signed token for x-guest-session header when cookie is not sent (e.g. after reload). */
  cookieValue?: string;
};

export const GUEST_SESSION_STORAGE_KEY = "bdai.guest.session";

const listeners = new Set<() => void>();

let cachedGuestSession: GuestSession | null | undefined;
let storageListenerAttached = false;

function isGuestSession(value: unknown): value is GuestSession {
  if (!value || typeof value !== "object") {
    return false;
  }
  const candidate = value as Record<string, unknown>;
  return (
    typeof candidate.guestId === "string" &&
    typeof candidate.issuedAt === "string" &&
    typeof candidate.expiresAt === "string"
  );
}

function readStoredGuestSession(): GuestSession | null {
  if (typeof window === "undefined") {
    return null;
  }

  const rawValue = window.localStorage.getItem(GUEST_SESSION_STORAGE_KEY);
  if (!rawValue) {
    return null;
  }

  try {
    const parsed = JSON.parse(rawValue) as unknown;
    return isGuestSession(parsed) ? parsed : null;
  } catch {
    return null;
  }
}

function notifyGuestSessionListeners() {
  for (const listener of listeners) {
    listener();
  }
}

function setCachedGuestSession(session: GuestSession | null) {
  cachedGuestSession = session;
  notifyGuestSessionListeners();
}

function ensureStorageListener() {
  if (storageListenerAttached || typeof window === "undefined") {
    return;
  }

  window.addEventListener("storage", (event) => {
    if (event.key !== GUEST_SESSION_STORAGE_KEY) {
      return;
    }

    cachedGuestSession = readStoredGuestSession();
    notifyGuestSessionListeners();
  });

  storageListenerAttached = true;
}

export function readGuestSession(): GuestSession | null {
  if (cachedGuestSession === undefined) {
    cachedGuestSession = readStoredGuestSession();
  }

  return cachedGuestSession;
}

export function writeGuestSession(session: GuestSession): void {
  if (typeof window === "undefined") {
    return;
  }

  window.localStorage.setItem(GUEST_SESSION_STORAGE_KEY, JSON.stringify(session));
  setCachedGuestSession(session);
}

export function clearGuestSession(): void {
  if (typeof window === "undefined") {
    return;
  }

  window.localStorage.removeItem(GUEST_SESSION_STORAGE_KEY);
  setCachedGuestSession(null);
}

export function isGuestSessionExpired(session: GuestSession | null): boolean {
  if (!session) {
    return true;
  }
  const expiresAtMs = Date.parse(session.expiresAt);
  return Number.isNaN(expiresAtMs) || expiresAtMs <= Date.now();
}

export function subscribeGuestSession(listener: () => void): () => void {
  ensureStorageListener();
  listeners.add(listener);

  return () => {
    listeners.delete(listener);
  };
}

export function useGuestSession(): GuestSession | null {
  return useSyncExternalStore(subscribeGuestSession, readGuestSession, () => null);
}
