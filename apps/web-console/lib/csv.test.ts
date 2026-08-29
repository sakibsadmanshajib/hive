import { describe, expect, it } from "vitest";

import { csvCell, toCsv } from "./csv";

/** The apostrophe sits inside the opening quote when a cell also needs quoting. */
function isNeutralised(cell: string): boolean {
  return cell.replace(/^"/, "").startsWith("'");
}

describe("csvCell", () => {
  it("neutralises a cell that opens with a formula character", () => {
    // Excel, LibreOffice Calc and Google Sheets evaluate a cell whose first
    // character is one of these. Issue #1401.
    for (const payload of [
      "=HYPERLINK(\"http://qa.invalid\",\"qa2\")",
      "=1+1",
      "+1+1",
      "-1+1",
      "@SUM(1)",
      "=cmd|'/c calc'!A0",
      "\tSUM(1)",
      "\r=1+1",
    ]) {
      expect(isNeutralised(csvCell(payload))).toBe(true);
    }
  });

  it("neutralises a formula hidden behind leading whitespace", () => {
    // A spreadsheet that trims a cell on import sees the payload the raw
    // first character hid. Testing only index 0 is the standard bypass.
    for (const payload of [
      " =1+1",
      "  \t@SUM(1)",
      "\n=cmd|'/c calc'!A0",
      " =1+1",
      "﻿=1+1",
    ]) {
      expect(isNeutralised(csvCell(payload))).toBe(true);
    }
  });

  it("keeps a numeric cell numeric, so the column still sums", () => {
    // The exemption is a property of the column, not of the value: a caller
    // passes a number for a column that holds numbers. Every ledger debit
    // opens with a minus sign, and prefixing those turns the column to text.
    expect(csvCell(-2000)).toBe("-2000");
    expect(csvCell(-12.5)).toBe("-12.5");
    expect(csvCell(0)).toBe("0");
    expect(csvCell(50_000_000)).toBe("50000000");
  });

  it("does not sniff a text cell for numbers, so a text value survives", () => {
    // "-001" in a text column is not a number the reader wants normalised.
    // Left unprefixed, a spreadsheet rewrites it to -1 and a save round trip
    // loses the original value.
    expect(csvCell("-001")).toBe("'-001");
    expect(csvCell("+1337")).toBe("'+1337");
  });

  it("quotes rather than destroys a cell containing a comma, quote or newline", () => {
    expect(csvCell("a,b")).toBe('"a,b"');
    expect(csvCell('say "hi"')).toBe('"say ""hi"""');
    expect(csvCell("one\ntwo")).toBe('"one\ntwo"');
  });

  it("quotes a neutralised cell that also carries a comma", () => {
    expect(csvCell("=A1,B1")).toBe("\"'=A1,B1\"");
  });

  it("passes ordinary text through untouched", () => {
    expect(csvCell("hive-free")).toBe("hive-free");
    expect(csvCell("")).toBe("");
  });
});

describe("toCsv", () => {
  it("joins the header and rows, neutralising text and leaving numbers alone", () => {
    const csv = toCsv(
      ["name", "delta"],
      [
        ["=1+1", -2000],
        ["ok", 5],
      ]
    );
    expect(csv).toBe("name,delta\n'=1+1,-2000\nok,5");
  });
});
