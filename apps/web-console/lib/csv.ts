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

const FORMULA_LEAD = /^[=+\-@\t\r]/;

// A plain number is exempt. Every debit in the billing ledger starts with a
// minus sign, and prefixing those turns a numeric column into text, which
// breaks SUM in the spreadsheet the export exists to feed. "-1+1" is not a
// number and is still neutralised.
const PLAIN_NUMBER = /^[+-]?(\d+(\.\d*)?|\.\d+)([eE][+-]?\d+)?$/;

const NEEDS_QUOTING = /[",\n\r]/;

/**
 * Escape one value for a CSV cell: neutralise a formula-leading payload with a
 * single quote, then quote and double any embedded quote per RFC 4180.
 */
export function csvCell(value: string): string {
  const neutralised =
    FORMULA_LEAD.test(value) && !PLAIN_NUMBER.test(value) ? `'${value}` : value;
  if (!NEEDS_QUOTING.test(neutralised)) return neutralised;
  return `"${neutralised.replace(/"/g, '""')}"`;
}

/** Join one row of already-stringified values. */
export function csvRow(cells: readonly string[]): string {
  return cells.map(csvCell).join(",");
}

/** Build a whole CSV document from a header row and its data rows. */
export function toCsv(
  header: readonly string[],
  rows: readonly (readonly string[])[]
): string {
  return [csvRow(header), ...rows.map(csvRow)].join("\n");
}
