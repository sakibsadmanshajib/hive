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
 * Format a credit quantity that arrives as a decimal string on the wire.
 *
 * BigInt, not Number, for the same reason formatTakaSubunits is: a credit is a
 * billionth of a dollar, so a large monthly quantity crosses
 * Number.MAX_SAFE_INTEGER long before the money does, and a silently rounded
 * money figure is exactly the class of defect issue #1681 exists to close.
 *
 * `null` is the unrecorded quantity, not zero. An invoice generated between the
 * issue #1648 fix and issue #1682's repair has a correct taka amount and no
 * credit count at all, and printing that as "0" would tell a customer they
 * consumed nothing in a month they were charged for. It renders as the same em
 * dash formatPercent and formatLatencyMs use for a genuine absence.
 */
export function formatCreditCount(
  value: string | null | undefined,
  locale: AppLocale = DEFAULT_LOCALE,
): string {
  if (value === null || value === undefined || value === "") {
    return "—";
  }
  let n: bigint;
  try {
    n = BigInt(value);
  } catch {
    return "—";
  }
  return new Intl.NumberFormat(intlTag(locale, "grouping")).format(n);
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

/**
 * One US dollar is one billion Hive credits (.wolf/decisions.md D-046,
 * migration 20260823_40_credit_unit_rescale_billion.sql).
 *
 * Nothing renders this. It is the conversion the PURCHASE flow needs, because
 * a customer buying credits is quoted a price in a currency they will actually
 * be charged (checkout-modal.tsx). Every balance, usage and spend surface
 * renders the credit quantity itself and no currency at all (D-070), so no
 * display formatter divides by this constant any more.
 */
export const CREDITS_PER_USD = 1_000_000_000;

/**
 * A credit quantity as digits, with no unit word.
 *
 * For the one place a pair of credit figures is read as a pair ("9,000 of
 * 10,000 credits"), where repeating the unit on both halves is noise. Every
 * other caller wants formatCreditAmount below, which states the unit.
 *
 * Locale is pinned to en-US rather than routed through intlTag. A credit
 * figure and its unit word are one string, the word is English on every
 * surface, and bn-BD's lakh grouping would put "10,00,00,00,000 credits" next
 * to an "10,000,000,000 credits" printed by the chat front end's twin of this
 * function, which carries no locale plumbing at all.
 *
 * Truncated rather than rounded: credits are whole in storage, so a
 * fractional value here can only be a decode artefact, and truncating never
 * invents a credit that is not there.
 */
export function formatCreditDigits(credits: number): string {
  if (!Number.isFinite(credits)) {
    return "—";
  }
  return new Intl.NumberFormat("en-US", {
    maximumFractionDigits: 0,
  }).format(Math.trunc(credits));
}

/**
 * A credit quantity as a customer reads it: the exact integer and its unit.
 *
 * This is the ONLY renderer for a balance, a usage figure, a spend figure or
 * a budget cap. It replaces the currency formatters those surfaces used to go
 * through, per the owner ruling recorded as .wolf/decisions.md D-070:
 * purchased credits display as Hive credits, and a currency figure appears
 * only on an invoice, which is a record of a payment actually made in that
 * currency.
 *
 * The ruling is a confidentiality boundary, not a style preference. Credits
 * are sold at a markup (D-065) and a subscription grants a credit quantity
 * whose internal value the owner requires stay unpublished, so a balance
 * rendered in dollars beside a price paid in dollars hands the customer the
 * peg and every internal figure that follows from it.
 *
 * There is no rounding to defend here, which is the point: the old balance
 * formatter floored to cents and the old price formatter rounded to nearest,
 * and keeping those two apart was its own recurring defect. An integer count
 * of credits is exact.
 *
 * The chat front end carries a twin at
 * vendor/open-webui/src/lib/hive/credits.ts. The two builds cannot share a
 * module, so tools/lint-credit-balance-formatter-parity.mjs fails the build
 * when they stop matching, which is how they diverged into #1344 and #1345.
 */
export function formatCreditAmount(credits: number): string {
  if (!Number.isFinite(credits)) {
    return "—";
  }
  const whole = Math.trunc(credits);
  const digits = new Intl.NumberFormat("en-US", {
    maximumFractionDigits: 0,
  }).format(whole);
  return digits + " " + (whole === 1 || whole === -1 ? "credit" : "credits");
}
