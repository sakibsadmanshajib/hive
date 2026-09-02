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
 * Defined here rather than in model-pricing.ts, which is where it used to
 * live: balances need it too, and model-pricing already imports this module,
 * so the reverse import would be a cycle. model-pricing re-exports it, so
 * existing callers are unaffected.
 */
export const CREDITS_PER_USD = 1_000_000_000;

/**
 * What a positive balance below one cent reads as. A bound, not a figure: at
 * two decimals such a balance would render "$0.00", which is what an empty
 * wallet renders, and the point of saying anything at all is that the wallet
 * is not empty.
 */
export const SUB_CENT_BALANCE = "< $0.01";

/**
 * Format a credit balance as the US dollars a customer reasons in.
 *
 * Rounded down, never up, which is the one behavioural difference from
 * formatUsdFromCredits in model-pricing.ts. A catalog rate is a published
 * price and rounds to the nearest cent; an available balance is spendable
 * money, and rounding 99,996,364,207 credits up to "$100.00" tells a customer
 * they hold more than they can spend. Down rather than toward zero, so that a
 * balance driven negative by reservations overstates the hole rather than
 * flattering it.
 *
 * Two decimals, always. A balance is money, and money reads in cents: the
 * per-value precision this used to borrow from the pricing formatter printed
 * a real balance of 858 credits as "$0.000000858", nine significant figures
 * in permanent chrome that no reader can act on. Nine decimals is the width
 * of one credit, which is the right precision for a published per-million
 * rate and the wrong precision for a wallet.
 *
 * What that precision was protecting is kept: a real, non-zero balance still
 * never renders as the "$0.00" an empty wallet renders. A positive balance
 * under one cent reads as SUB_CENT_BALANCE, a bound rather than a figure. A
 * negative one needs no such case, because flooring moves it away from zero:
 * one credit overdrawn already floors to "-$0.01".
 *
 * Locale is pinned to en-US for the same reason model-pricing pins it: the
 * unit is US dollars on every surface, and bn-BD renders the same amount as
 * "US$0.20", which reads as a second currency.
 *
 * The chat front end carries a byte-identical twin at
 * vendor/open-webui/src/lib/hive/credits.ts. The two builds cannot share a
 * module, so tools/lint-credit-balance-formatter-parity.mjs fails the build
 * when they stop matching, which is how they diverged into #1344 and #1345.
 */
export function formatUsdBalanceFromCredits(credits: number): string {
  // Zero is a real, readable balance. A non-finite value is not: it can only
  // come from a decode that failed, and rendering that as "$0.00" would assert
  // an empty wallet where nothing was read at all. Same policy, and the same
  // em dash, as formatPercent above.
  if (!Number.isFinite(credits)) {
    return "—";
  }
  if (credits === 0) {
    return "$0.00";
  }
  // Floor in credits rather than in dollars. The dollar product is a float:
  // 8,290,000,000 credits is 8.29 dollars, 8.29 times 100 is
  // 828.9999999999999, and flooring that prints $8.28, understating a real
  // balance by a cent. CREDITS_PER_USD is a power of ten, so creditsPerCent is
  // an exact integer and this is exact for every integer balance.
  const creditsPerCent = CREDITS_PER_USD / 100;
  const cents = Math.floor(credits / creditsPerCent);
  if (cents === 0) {
    return SUB_CENT_BALANCE;
  }
  return new Intl.NumberFormat("en-US", {
    style: "currency",
    currency: "USD",
    minimumFractionDigits: 2,
    maximumFractionDigits: 2,
  }).format(cents / 100);
}
