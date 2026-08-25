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
