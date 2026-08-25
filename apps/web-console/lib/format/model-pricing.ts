import { formatCredits } from "@/lib/format/credits";

/**
 * One US dollar is one billion Hive credits (`.wolf/decisions.md` D-046,
 * migration `20260823_40_credit_unit_rescale_billion.sql`). Every catalog
 * price column stores an integer credit rate per one million metered tokens,
 * so dividing by this constant yields the dollars-per-million figure a
 * customer recognises.
 */
export const CREDITS_PER_USD = 1_000_000_000;

/**
 * Which unit a price cell renders in. Both units share one absence policy,
 * because "deliberately free", "variable by design" and "we could not read
 * the price" mean the same three things whichever unit the number is in.
 */
export type PriceUnit = "usd" | "credits";

/**
 * Render a credit rate as US dollars without ever rounding a real price down
 * to zero.
 *
 * The precision is chosen per value rather than fixed at two decimals. A
 * two-decimal render prints `$0.00` for a cache-read rate of 2,982,000
 * credits per million ($0.002982, the corrected DeepSeek flash rate from
 * `20260825_02_deepseek_cache_read_price_correction.sql`), and a price that
 * looks free is a worse lie than the raw integer this replaces.
 *
 * The rule: keep three significant digits, never fewer than two decimals, and
 * never more than nine. Nine is not a taste call, it is the exact width of
 * the unit: one credit is 1e-9 USD, so nine decimals renders ANY integer
 * credit rate as a non-zero figure. `$0.00` is therefore unreachable for a
 * non-zero price, which is the invariant the tests pin.
 *
 * Locale is pinned to en-US rather than routed through `intlTag`. The unit is
 * US dollars on every surface, and `bn-BD` renders the same amount as
 * `US$0.20`, which reads as a second currency next to the `$0.20` the model
 * pages this mirrors print. Grouping separators only appear above $1,000,
 * which no per-million token rate in the catalog approaches.
 */
export function formatUsdFromCredits(credits: number): string {
  if (!Number.isFinite(credits) || credits === 0) {
    return "$0";
  }
  const usd = credits / CREDITS_PER_USD;
  const magnitude = Math.floor(Math.log10(Math.abs(usd)));
  const maximumFractionDigits = Math.min(9, Math.max(2, 2 - magnitude));
  return new Intl.NumberFormat("en-US", {
    style: "currency",
    currency: "USD",
    minimumFractionDigits: 2,
    maximumFractionDigits,
  }).format(usd);
}

function renderPrice(credits: number, unit: PriceUnit): string {
  return unit === "credits" ? formatCredits(credits) : formatUsdFromCredits(credits);
}

/**
 * Render one catalog price for display. Three outcomes, deliberately distinct,
 * because on a pricing surface they mean three different things:
 *
 *   - A number, zero included, is a published rate. Zero means the dimension is
 *     deliberately not charged (hive-default publishes exactly that for cache
 *     write), so it prints as "$0" and never as an absence. A customer reading
 *     zero is reading a decision.
 *   - Null on an `upstream_actual` alias is the design: the price is variable
 *     per request and there genuinely is no per-million rate to publish.
 *   - Null on a `fixed` alias means the lookup came back empty. Calling that
 *     "Variable" would dress a broken decode up as a pricing model on the one
 *     screen a customer opens to check what a model costs.
 *
 * `unit` changes only how a real number is drawn. It cannot turn an absence
 * into a number or a number into an absence.
 */
export function formatModelPrice(
  credits: number | null,
  pricingMode: string,
  unit: PriceUnit = "usd",
): string {
  if (credits === null) {
    return pricingMode === "upstream_actual" ? "Variable" : "Unknown";
  }
  return renderPrice(credits, unit);
}

/**
 * Cache prices carry a fourth outcome the input and output dimensions do not.
 * A fixed-price alias that publishes no cache rate is neither broken nor
 * variable, it simply has no cache rate to charge, and the model pages this
 * mirrors render exactly that case as a dash in their own `Cache read /M`
 * column. Rendering zero instead would say the alias caches for free, which is
 * the expensive kind of wrong on a pricing screen. A variable-price alias
 * still reads "Variable", because its cache component is variable for the same
 * reason its input is.
 */
export function formatCachePrice(
  credits: number | null,
  pricingMode: string,
  unit: PriceUnit = "usd",
): string {
  if (credits === null) {
    return pricingMode === "upstream_actual" ? "Variable" : "—";
  }
  return renderPrice(credits, unit);
}

/**
 * The combined "in / out" figure shown in the model header tile, mirroring the
 * shape the model pages this parity-matches put in the same position. Both
 * halves go through formatModelPrice, so a variable-price alias reads
 * "Variable / Variable" rather than borrowing a number from the other side.
 */
export function formatInOutPrice(
  pricing: {
    input_price_credits: number | null;
    output_price_credits: number | null;
    pricing_mode: string;
  },
  unit: PriceUnit = "usd",
): string {
  const input = formatModelPrice(
    pricing.input_price_credits,
    pricing.pricing_mode,
    unit,
  );
  const output = formatModelPrice(
    pricing.output_price_credits,
    pricing.pricing_mode,
    unit,
  );
  return `${input} / ${output}`;
}
