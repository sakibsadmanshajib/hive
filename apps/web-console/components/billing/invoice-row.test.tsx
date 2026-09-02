/**
 * InvoiceRow renders the server-side invoice table row. Its money-relevant
 * contract is the download link: it must point at the per-invoice PDF proxy
 * for exactly the right invoice id.
 */
import { describe, expect, it, vi } from "vitest";
import { render, screen } from "@testing-library/react";

vi.mock("next/link", () => ({
  default: ({ href, children }: { href: string; children?: React.ReactNode }) => (
    <a href={href} data-testid="invoice-pdf-link">
      {children}
    </a>
  ),
}));

import { InvoiceRow } from "./invoice-row";
import type { InvoiceRecord } from "@/lib/control-plane/client";

function recordFixture(overrides: Partial<InvoiceRecord> = {}): InvoiceRecord {
  return {
    id: "inv-4021",
    workspace_id: "ws-1",
    period_start: "2026-07-01",
    period_end: "2026-07-31",
    total_credits: "1150000000",
    line_items: [],
    generated_at: "2026-08-01T00:00:00Z",
    ...overrides,
  };
}

describe("InvoiceRow", () => {
  it("download link targets the pdf proxy endpoint for this invoice id", () => {
    render(
      <table>
        <tbody>
          <InvoiceRow invoice={recordFixture()} />
        </tbody>
      </table>,
    );
    const link = screen.getByTestId("invoice-pdf-link");
    expect(link.getAttribute("href")).toBe("/api/invoices/inv-4021/pdf");
  });

  // Issue #1681 plus the owner's 2026-09-02 amendment. The quantity is the one
  // measured live, 524,653,338 credits. No money appears at all: a usage period
  // is a prepaid draw down that raises no charge, and pricing the quantity at
  // the internal peg would disclose the internal value of a subscription's
  // credit grant.
  it("renders the credit quantity and no money figure", () => {
    render(
      <table>
        <tbody>
          <InvoiceRow invoice={recordFixture({ total_credits: "524653338" })} />
        </tbody>
      </table>,
    );
    const text = screen.getByTestId("invoice-pdf-link").closest("tr")
      ?.textContent ?? "";

    expect(text).toContain("524,653,338");
    // The original defect: the credit count divided by one hundred as taka.
    expect(text).not.toContain("5,246,533.38");
    // The amendment: no currency marker of any kind on this surface.
    expect(text).not.toContain("৳");
    expect(text).not.toContain("$");
    expect(text).not.toMatch(/BDT|USD|taka/i);
  });

  it("renders an unrecorded credit quantity as absent, never as zero", () => {
    render(
      <table>
        <tbody>
          <InvoiceRow invoice={recordFixture({ total_credits: null })} />
        </tbody>
      </table>,
    );
    // Assert on the credits cell, not the whole row: the row legitimately
    // says "0 models" for an empty line-item list, and a row-wide check for a
    // zero would fail on that instead of on the quantity it is guarding.
    const cells = screen.getByTestId("invoice-pdf-link").closest("tr")
      ?.querySelectorAll("td") ?? [];
    const credits = cells[1]?.textContent?.trim() ?? "";
    expect(credits).toBe("—");
  });

  it("renders the period range and model count", () => {
    render(
      <table>
        <tbody>
          <InvoiceRow invoice={recordFixture()} />
        </tbody>
      </table>,
    );
    expect(screen.getByText(/2026-07-01/)).toBeTruthy();
    const modelsCell = screen.getByText(/models/i).closest("td");
    expect(modelsCell?.textContent?.replace(/\s+/g, " ").trim()).toBe("0 models");
  });
});
