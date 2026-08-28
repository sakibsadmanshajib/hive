// One-cell escaping for the hand-built GitHub-flavoured markdown tables the
// live walkthrough harnesses emit.
//
// Escaping only `|` is the bug CodeQL flags as js/incomplete-sanitization
// (high): input that already contains a backslash comes out as `\\|`, which
// GFM reads as a literal backslash followed by an unescaped cell separator,
// so an observation string can still inject columns into the report. A
// newline does the same thing one row up: GFM ends a table row at the
// newline, so a multi-line error message silently shreds the rest of the
// table. Both are escaped here, in one pass over the input, so there is no
// second pass that can re-escape what the first one wrote.

/**
 * Escapes one value for use inside a GFM table cell.
 *
 * @param {unknown} value raw cell content, may be null/undefined
 * @param {{ max?: number }} [options] `max` truncates the escaped result
 * @returns {string} safe cell text, never containing a raw `|` or newline
 */
export function mdTableCell(value, { max } = {}) {
  const escaped = String(value ?? "")
    .replace(/\r\n|\r|\n/g, " ")
    .replace(/[\\|]/g, "\\$&");
  if (typeof max !== "number") return escaped;
  // Truncating escaped text can sever a backslash from the character it
  // escapes, and a cell ending in a lone backslash escapes the row's own
  // closing `|`, which is the same column injection by another route. Drop
  // the orphan.
  const sliced = escaped.slice(0, max);
  const trailing = /\\*$/.exec(sliced)[0].length;
  return trailing % 2 === 0 ? sliced : sliced.slice(0, -1);
}
