/**
 * Format integer credit values for display in the console. Credits are
 * always whole numbers in storage; format them with thousand-separators
 * so 12,345 reads cleanly in the dashboard, billing tables, and
 * receipts.
 */
export function formatCredits(value: number): string {
  if (!Number.isFinite(value)) {
    return "0";
  }
  return new Intl.NumberFormat("en-US", { maximumFractionDigits: 0 }).format(
    Math.trunc(value),
  );
}

/**
 * Format a token count (request totals, completion tokens, etc.). Same
 * thousand-separator behaviour, distinct semantic name so callers can
 * see at a glance which scalar they are formatting.
 */
export function formatTokens(value: number): string {
  return formatCredits(value);
}

/**
 * Format an ISO date string as a short day/month/year for tables.
 * Returns an em-dash for null/undefined/empty values so columns line
 * up visually.
 */
export function formatShortDate(value: string | null | undefined): string {
  if (!value) {
    return "—";
  }
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) {
    return value;
  }
  return new Intl.DateTimeFormat("en-US", {
    year: "numeric",
    month: "short",
    day: "numeric",
  }).format(date);
}
