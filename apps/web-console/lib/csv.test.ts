import { describe, expect, it } from "vitest";

import { csvCell, toCsv } from "./csv";

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
      // A payload that also needs RFC 4180 quoting comes back wrapped, so the
      // apostrophe sits just inside the opening quote rather than at index 0.
      expect(csvCell(payload).replace(/^"/, "").startsWith("'")).toBe(true);
    }
  });

  it("leaves a negative or signed number alone so a spreadsheet still sums it", () => {
    // A leading "-" is also how every debit in the billing ledger starts, so
    // blanket-prefixing turns a numeric column into text and breaks SUM.
    expect(csvCell("-2000")).toBe("-2000");
    expect(csvCell("-12.5")).toBe("-12.5");
    expect(csvCell("+7")).toBe("+7");
    expect(csvCell("-1e9")).toBe("-1e9");
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
  it("joins the header and rows and neutralises every cell", () => {
    const csv = toCsv(
      ["name", "delta"],
      [
        ["=1+1", "-2000"],
        ["ok", "5"],
      ]
    );
    expect(csv).toBe("name,delta\n'=1+1,-2000\nok,5");
  });
});
