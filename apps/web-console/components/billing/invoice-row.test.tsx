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
    total_bdt_subunits: "11500",
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

  // Issue #1681, acceptance criterion 3. The fixture is the magnitude the owner
  // saw live: 524,653,338 credits, charged as BDT 52.47. The two figures are
  // deliberately far apart, so a row that derived either from the other by the
  // wrong factor cannot pass.
  it("renders the credit quantity and the charged amount as separate figures", () => {
    render(
      <table>
        <tbody>
          <InvoiceRow
            invoice={recordFixture({
              total_credits: "524653338",
              total_bdt_subunits: "5247",
            })}
          />
        </tbody>
      </table>,
    );
    const text = screen.getByTestId("invoice-pdf-link").closest("tr")
      ?.textContent ?? "";

    expect(text).toContain("524,653,338");
    expect(text).toContain("৳52.47");
    // The defect: the credit count divided by one hundred and printed as taka.
    expect(text).not.toContain("5,246,533.38");
    // The inverse: the taka amount inflated and presented as the quantity.
    expect(text).not.toContain("524,700");
  });

  it("renders an unrecorded credit quantity as absent, never as zero", () => {
    render(
      <table>
        <tbody>
          <InvoiceRow
            invoice={recordFixture({
              total_credits: null,
              total_bdt_subunits: "5247",
            })}
          />
        </tbody>
      </table>,
    );
    const text = screen.getByTestId("invoice-pdf-link").closest("tr")
      ?.textContent ?? "";
    expect(text).toContain("৳52.47");
    expect(text).toContain("—");
    expect(text).not.toMatch(/(^|\D)0 credits/);
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
