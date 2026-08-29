import {
  DEFAULT_LOCALE,
  DISPLAY_TIME_ZONE,
  intlTag,
  type AppLocale,
} from "@/lib/i18n/locales";

/**
 * Format integer credit values for display in the console. Credits are
 * always whole numbers in storage; format them with thousand-separators
 * so 12,345 reads cleanly in the dashboard, billing tables, and
 * receipts.
 */
export function formatCredits(
  value: number,
  locale: AppLocale = DEFAULT_LOCALE,
): string {
  if (!Number.isFinite(value)) {
    return "0";
  }
  return new Intl.NumberFormat(intlTag(locale, "number"), {
    maximumFractionDigits: 0,
  }).format(Math.trunc(value));
}

/**
 * Format a token count (request totals, completion tokens, etc.). Same
 * thousand-separator behaviour, distinct semantic name so callers can
 * see at a glance which scalar they are formatting.
 */
export function formatTokens(
  value: number,
  locale: AppLocale = DEFAULT_LOCALE,
): string {
  return formatCredits(value, locale);
}

/**
 * Format an arbitrary numeric metric, keeping fractional digits when the
 * value carries them (latency percentiles, rates in analytics tables).
 */
export function formatNumber(
  value: number,
  locale: AppLocale = DEFAULT_LOCALE,
): string {
  if (!Number.isFinite(value)) {
    return "0";
  }
  return new Intl.NumberFormat(intlTag(locale, "number")).format(value);
}

/**
 * Format a request's round-trip latency in milliseconds as a short duration
 * string ("340ms" under one second, "1.2s" at or above it).
 *
 * Takes `number | null` because latency is genuinely unknown for a request
 * whose attempt has no completed_at yet (still dispatching or streaming),
 * not a measured zero. Callers render the em-dash returned here as the
 * visible absence, same convention as formatPercent below.
 */
export function formatLatencyMs(
  value: number | null,
  locale: AppLocale = DEFAULT_LOCALE,
): string {
  if (value === null || !Number.isFinite(value) || value < 0) {
    return "—";
  }
  // Round before choosing the unit, not after: picking on the raw value
  // sends 999.7 down the millisecond branch and then rounds it to a
  // "1000ms" that the second branch would have written as "1.0s".
  const roundedMs = Math.round(value);
  if (roundedMs < 1000) {
    return `${roundedMs}ms`;
  }
  return `${new Intl.NumberFormat(intlTag(locale, "number"), {
    minimumFractionDigits: 1,
    maximumFractionDigits: 1,
  }).format(value / 1000)}s`;
}

/**
 * Format an ISO date string as a short day/month/year for tables — day-first
 * (e.g. "25 Apr 2026") for BD-market consistency. Returns an em-dash for
 * null/undefined/empty values so columns line up visually.
 */
export function formatShortDate(
  value: string | null | undefined,
  locale: AppLocale = DEFAULT_LOCALE,
): string {
  if (!value) {
    return "—";
  }
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) {
    return value;
  }
  return new Intl.DateTimeFormat(intlTag(locale, "date"), {
    year: "numeric",
    month: "short",
    day: "numeric",
    // Pin to Asia/Dhaka so SSR (Cloudflare Workers UTC) and CSR
    // (browser local) render identical days near midnight UTC. This
    // also keeps date columns visually consistent for the BD-market
    // audience this console targets.
    timeZone: DISPLAY_TIME_ZONE,
  }).format(date);
}

/**
 * Format a 0..1 ratio as a percentage with one decimal place ("96.0%").
 *
 * Takes `number | null` because the rates this renders are genuinely absent
 * on an empty sample, and a null that silently became "0.0%" would assert a
 * measured zero-percent cache hit rate where nothing was measured at all.
 * Callers render the em-dash as the visible absence.
 */
export function formatPercent(
  value: number | null,
  locale: AppLocale = DEFAULT_LOCALE,
): string {
  if (value === null || !Number.isFinite(value)) {
    return "—";
  }
  return new Intl.NumberFormat(intlTag(locale, "number"), {
    style: "percent",
    minimumFractionDigits: 1,
    maximumFractionDigits: 1,
  }).format(value);
}
