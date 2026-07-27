import {
  DEFAULT_LOCALE,
  intlTag,
  type AppLocale,
} from "@/lib/i18n/locales";

/**
 * Render a minor-unit amount in its own rail currency.
 *
 * REGULATORY: never display USD equivalents, FX rates, or any conversion
 * language to BD accounts. Callers pass the rail's currency and nothing
 * else — there is deliberately no fallback to "USD" here.
 */
export function formatCurrency(
  amountCents: number,
  currency: string,
  locale: AppLocale = DEFAULT_LOCALE,
): string {
  return new Intl.NumberFormat(intlTag(locale, "number"), {
    style: "currency",
    currency,
    minimumFractionDigits: 2,
  }).format(amountCents / 100);
}

/**
 * Render a taka total that arrives as a subunit string.
 *
 * BigInt math — the wire shape is a JSON string (Go `,string` tag) so totals
 * beyond Number.MAX_SAFE_INTEGER (2^53−1) preserve full BIGINT precision. A
 * Number-based path silently rounds very large monthly totals, so the bigint
 * is handed to Intl as-is.
 *
 * Uses the Bangla taka glyph (৳) directly rather than `style: "currency"`,
 * because the regulatory rule is BDT only and the glyph must never be
 * substituted for a currency code by locale data.
 */
export function formatTakaSubunits(
  subunits: string,
  locale: AppLocale = DEFAULT_LOCALE,
): string {
  let n: bigint;
  try {
    n = BigInt(subunits);
  } catch {
    return "৳0.00";
  }
  if (n < 0n) return "৳0.00";
  const integer = n / 100n;
  const fraction = n % 100n;
  // Intl formats a bigint directly, so grouping survives past
  // Number.MAX_SAFE_INTEGER without a lossy Number() hop.
  const integerDisplay = new Intl.NumberFormat(
    intlTag(locale, "grouping"),
  ).format(integer);
  return `৳${integerDisplay}.${fraction.toString().padStart(2, "0")}`;
}
