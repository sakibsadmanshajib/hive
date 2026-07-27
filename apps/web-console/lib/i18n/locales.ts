/**
 * Supported console locales. The active locale lives in a cookie rather than
 * a URL segment: `middleware.ts` already chains Supabase SSR session refresh,
 * the email-verification gate, and CSP headers, and stacking next-intl's
 * routing middleware on top of that is the one documented way to break
 * `@opennextjs/cloudflare`. Cookie-based locale keeps middleware untouched.
 */
export const LOCALES = ["en", "bn"] as const;

export type AppLocale = (typeof LOCALES)[number];

export const DEFAULT_LOCALE: AppLocale = "en";

export const LOCALE_COOKIE = "locale";

/** Short switcher labels. Language names are written in their own language. */
export const LOCALE_LABELS: Record<AppLocale, string> = {
  en: "EN",
  bn: "বাংলা",
};

export function isAppLocale(value: string): value is AppLocale {
  return (LOCALES as readonly string[]).includes(value);
}

/**
 * Resolve an untrusted cookie value to a supported locale.
 */
export function resolveLocale(value: string | undefined | null): AppLocale {
  return value && isAppLocale(value) ? value : DEFAULT_LOCALE;
}

/**
 * Map an app locale to the BCP-47 tag handed to `Intl`.
 *
 * `bn-BD` alone resolves to `numberingSystem: "beng"`, which renders credit
 * balances in Bengali digits (১২,৩৪,৫৬৭). The `-u-nu-latn` extension pins
 * Latin digits while keeping Bengali month names and lakh/crore grouping,
 * so money stays legible to anyone reading a receipt or an invoice.
 *
 * `en` deliberately keeps three distinct regional tags, one per surface,
 * because each matches what that surface renders today:
 *   - `number`   -> en-US  (1,234,567 grouping for credits and tokens)
 *   - `date`     -> en-GB  (day-first, "27 Jul 2026")
 *   - `grouping` -> en-BD  (lakh/crore grouping used by the taka amounts)
 * Collapsing them changes visible output, so that is left to the billing
 * phase where the numbers can be reviewed on screen.
 */
export type IntlSurface = "number" | "date" | "grouping";

const EN_TAGS: Record<IntlSurface, string> = {
  number: "en-US",
  date: "en-GB",
  grouping: "en-BD",
};

export function intlTag(locale: AppLocale, surface: IntlSurface): string {
  return locale === "bn" ? "bn-BD-u-nu-latn" : EN_TAGS[surface];
}

/** Every console timestamp renders in Dhaka time, regardless of locale. */
export const DISPLAY_TIME_ZONE = "Asia/Dhaka";
