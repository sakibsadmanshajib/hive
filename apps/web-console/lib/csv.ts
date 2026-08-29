// One CSV writer for every console export, so a cell is escaped the same way
// wherever it is written. Before this existed the logs export and the billing
// ledger export each had their own hand-rolled join, each with a different
// bug: one replaced quotes, commas and newlines with spaces (destroying the
// value it was meant to export), the other stripped commas from a single
// column and escaped nothing else.
//
// Issue #1401: a cell whose first character is "=", "+", "-", "@", a tab or a
// carriage return is evaluated as a formula by Excel, LibreOffice Calc and
// Google Sheets when the file is opened. The console's own export is the
// delivery vehicle, and in a multi-member workspace the person who names an
// API key and the person who opens the export are not the same person.

// Leading whitespace is skipped before the first character is tested. A
// spreadsheet that trims a cell on import sees the payload that " =1+1" hides
// from a check anchored at index 0, and that is the standard bypass.
// JavaScript's \s already covers the byte order mark and a non-breaking space
// alongside the ASCII whitespace, and it covers the tab and the carriage
// return that are themselves leading characters worth neutralising.
const FORMULA_LEAD = /^\s*[=+\-@\t\r]/;

const NEEDS_QUOTING = /[",\n\r]/;

/**
 * A cell is either text, which is always neutralised, or a number, which is
 * never neutralised.
 *
 * The exemption is a property of the column rather than of the value, which is
 * why it travels as a type instead of a regular expression that sniffs the
 * string. Every debit in the billing ledger opens with a minus sign, so
 * prefixing those would turn a numeric column into text and break SUM in the
 * spreadsheet the export exists to feed. Sniffing instead would exempt a text
 * value that merely looks numeric, and "-001" in a text column is not a number
 * the reader wants normalised to -1 on the next save.
 */
export type CsvValue = string | number;

/**
 * Escape one value for a CSV cell: neutralise a formula-leading text payload
 * with a single quote, then quote and double any embedded quote per RFC 4180.
 */
export function csvCell(value: CsvValue): string {
  if (typeof value === "number") return String(value);
  const neutralised = FORMULA_LEAD.test(value) ? `'${value}` : value;
  if (!NEEDS_QUOTING.test(neutralised)) return neutralised;
  return `"${neutralised.replace(/"/g, '""')}"`;
}

/** Join one row of values. */
export function csvRow(cells: readonly CsvValue[]): string {
  return cells.map(csvCell).join(",");
}

/**
 * Build a whole CSV document from a header row and its data rows.
 *
 * Records are separated by CRLF, which is what RFC 4180 specifies and what
 * this writer's quoting already claims to follow. A cell's own bytes are left
 * exactly as they arrived: a line break inside a quoted field is not rewritten
 * to CRLF, because that would alter the stored value on a round trip, which is
 * the mutation this writer exists to avoid.
 */
export function toCsv(
  header: readonly string[],
  rows: readonly (readonly CsvValue[])[]
): string {
  return [csvRow(header), ...rows.map(csvRow)].join("\r\n");
}
