import { useCallback, useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';

/**
 * First-run language picker state hook (Hive Phase 19).
 *
 * Persists user's locale choice to localStorage on first visit so the picker
 * modal blocks only once per browser. Phase 20 will bridge this hook to
 * Supabase user prefs once auth swap lands.
 *
 * Storage key: `hive_locale_v1`
 * Supported locales: bn-BD (Bengali — Bangladesh), en-US (English — US alias for upstream `en`)
 *
 * Note on locale code mapping:
 *   - Upstream LibreChat v0.7.9 i18n resource is keyed `en` (NOT `en-US`).
 *   - Hive picker exposes the canonical `en-US` code for clarity, then maps to
 *     the actual i18next resource key via `resolveResourceKey`.
 */

const LOCALE_KEY = 'hive_locale_v1';

export type SupportedLocale = 'bn-BD' | 'en-US';
const SUPPORTED: SupportedLocale[] = ['bn-BD', 'en-US'];

/** Map Hive picker codes → upstream i18next resource keys. */
function resolveResourceKey(code: SupportedLocale): string {
  if (code === 'en-US') {
    return 'en';
  }
  return code;
}

function readPersisted(): SupportedLocale | null {
  if (typeof window === 'undefined') {
    return null;
  }
  const v = window.localStorage.getItem(LOCALE_KEY);
  return SUPPORTED.includes(v as SupportedLocale) ? (v as SupportedLocale) : null;
}

export function useFirstRunLocale() {
  const { i18n } = useTranslation();
  const [locale, setLocaleState] = useState<SupportedLocale | null>(readPersisted);

  const isFirstRun = locale === null;

  const setLocale = useCallback(
    (next: SupportedLocale) => {
      if (!SUPPORTED.includes(next)) {
        return;
      }
      try {
        window.localStorage.setItem(LOCALE_KEY, next);
      } catch {
        // localStorage unavailable (private mode, quota); fall through — language switch still applies.
      }
      setLocaleState(next);
      void i18n.changeLanguage(resolveResourceKey(next));
      // Phase 20 bridge: forward `next` to Supabase user prefs once auth swap lands.
    },
    [i18n],
  );

  useEffect(() => {
    if (locale && i18n.language !== resolveResourceKey(locale)) {
      void i18n.changeLanguage(resolveResourceKey(locale));
    }
  }, [locale, i18n]);

  return { locale, setLocale, isFirstRun };
}

export const HIVE_LOCALE_STORAGE_KEY = LOCALE_KEY;
