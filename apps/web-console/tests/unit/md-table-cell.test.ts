import { describe, expect, it } from "vitest";

import { mdTableCell } from "../e2e/support/md-table.mjs";

// The live walkthrough harness builds its report.md table by hand out of
// whatever a live page said, including raw error text. Escaping only `|` was
// flagged by CodeQL as js/incomplete-sanitization (high): a backslash in the
// input survives unescaped, and so does a newline, and either one lets an
// observation string break out of its cell.

describe("mdTableCell", () => {
  it("escapes a pipe so it cannot end the cell", () => {
    expect(mdTableCell("Chat | Cowork")).toBe("Chat \\| Cowork");
  });

  it("escapes the backslash the original single-pass replace left behind", () => {
    // `\|` used to become `\\|`: a literal backslash, then a live separator.
    expect(mdTableCell("path\\|next")).toBe("path\\\\\\|next");
  });

  it("flattens newlines, which end a table row in GFM", () => {
    expect(mdTableCell("line one\nline two\r\nline three")).toBe(
      "line one line two line three",
    );
  });

  it("flattens the unicode line separators too", () => {
    expect(mdTableCell("one\u2028two\u2029three")).toBe("one two three");
  });

  it("never leaves an orphan backslash when truncating", () => {
    // Cut lands exactly on the escape character; keeping it would escape the
    // row's own closing pipe.
    const cell = mdTableCell("ab|cd", { max: 3 });
    expect(cell).toBe("ab");
    expect(cell.endsWith("\\")).toBe(false);
  });

  it("keeps a fully escaped backslash pair intact when truncating", () => {
    expect(mdTableCell("a\\\\b", { max: 3 })).toBe("a\\\\");
  });

  it("renders null and undefined as an empty cell", () => {
    expect(mdTableCell(null)).toBe("");
    expect(mdTableCell(undefined)).toBe("");
  });
});
