import {
  DEFAULT_LOCALE,
  DISPLAY_TIME_ZONE,
  intlTag,
  type AppLocale,
} from "@/lib/i18n/locales";

/**
 * Format an ISO timestamp with a short month plus wall-clock time, for
 * ledger and alert rows. Falls back to the raw input on unparseable values
 * so a bad row shows its payload instead of "Invalid Date".
 */
export function formatDateTime(
  isoString: string,
  locale: AppLocale = DEFAULT_LOCALE,
): string {
  const date = new Date(isoString);
  if (Number.isNaN(date.getTime())) {
    return isoString;
  }
  return new Intl.DateTimeFormat(intlTag(locale, "date"), {
    year: "numeric",
    month: "short",
    day: "numeric",
    hour: "2-digit",
    minute: "2-digit",
    timeZone: DISPLAY_TIME_ZONE,
  }).format(date);
}

/**
 * Format an ISO timestamp as a spelled-out date for documents a customer
 * keeps (invoice PDFs), where an unambiguous month beats a compact column.
 */
export function formatLongDate(
  isoString: string,
  locale: AppLocale = DEFAULT_LOCALE,
): string {
  const date = new Date(isoString);
  if (Number.isNaN(date.getTime())) {
    return isoString;
  }
  return new Intl.DateTimeFormat(intlTag(locale, "date"), {
    year: "numeric",
    month: "long",
    day: "numeric",
    timeZone: DISPLAY_TIME_ZONE,
  }).format(date);
}
