/**
 * Behavioral tests for the client-side invoice PDF download button.
 * The @react-pdf/renderer dependency is mocked at the module boundary. The
 * suite proves the user-visible contract: clicking Download PDF produces a
 * blob download named invoice-{id}.pdf, guards against double clicks while
 * generation runs, and degrades to an alert on failure.
 */
import { afterEach, describe, expect, it, vi } from "vitest";
import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";

const pdfHarness = vi.hoisted(() => {
  const harness = {
    resolvers: new Array<(blob: Blob) => void>(),
    failNext: false,
    defer: false,
  };
  return harness;
});

vi.mock("@react-pdf/renderer", () => {
  function StubNode() {
    return null;
  }
  return {
    Document: StubNode,
    Page: StubNode,
    View: StubNode,
    Text: StubNode,
    StyleSheet: { create: (styles: unknown) => styles },
    pdf: () => ({
      toBlob: () =>
        new Promise<Blob>((resolve, reject) => {
          if (pdfHarness.failNext) {
            reject(new Error("render exploded"));
            return;
          }
          if (pdfHarness.defer) {
            pdfHarness.resolvers.push(resolve);
            return;
          }
          resolve(new Blob(["%PDF-fake"], { type: "application/pdf" }));
        }),
    }),
  };
});

import { InvoiceDownloadButton } from "./invoice-download-button";
import type { Invoice } from "@/lib/control-plane/client";

function invoiceFixture(overrides: Partial<Invoice> = {}): Invoice {
  return {
    id: "inv-9",
    invoice_number: "INV-009",
    status: "paid",
    credits: 1_000_000_000,
    amount_local: 11_500,
    local_currency: "BDT",
    tax_treatment: "VAT registered",
    rail: "bkash",
    line_items: [],
    created_at: "2026-08-20T10:00:00Z",
    ...overrides,
  };
}

afterEach(() => {
  cleanup();
  Reflect.deleteProperty(URL, "createObjectURL");
  Reflect.deleteProperty(URL, "revokeObjectURL");
  Reflect.deleteProperty(window, "alert");
  pdfHarness.resolvers.length = 0;
  pdfHarness.failNext = false;
  pdfHarness.defer = false;
  vi.restoreAllMocks();
});

function installDownloadCapture(): Array<{ download: string | null }> {
  const anchors: Array<{ download: string | null }> = [];
  vi.spyOn(HTMLAnchorElement.prototype, "click").mockImplementation(function mockClick(
    this: HTMLAnchorElement,
  ) {
    anchors.push({ download: this.getAttribute("download") });
  });
  Object.defineProperty(URL, "createObjectURL", {
    value: () => "blob:invoice-mock",
    configurable: true,
    writable: true,
  });
  Object.defineProperty(URL, "revokeObjectURL", {
    value: () => {},
    configurable: true,
    writable: true,
  });
  return anchors;
}

describe("InvoiceDownloadButton", () => {
  it("download completes and returns the button to its idle label", async () => {
    installDownloadCapture();
    const blobs: Blob[] = [];
    Object.defineProperty(URL, "createObjectURL", {
      value: (blob: Blob) => {
        blobs.push(blob);
        return "blob:invoice-mock";
      },
      configurable: true,
      writable: true,
    });

    render(<InvoiceDownloadButton invoice={invoiceFixture()} />);
    fireEvent.click(screen.getByRole("button", { name: /download pdf/i }));

    await waitFor(() => {
      expect(blobs.length).toBeGreaterThan(0);
    });
    expect(blobs[0].type).toBe("application/pdf");
    const button = screen.getByRole("button", { name: /download pdf/i });
    expect(button.textContent).toContain("Download PDF");
  });

  it("uses the invoice id in the download filename", async () => {
    const anchors = installDownloadCapture();
    render(<InvoiceDownloadButton invoice={invoiceFixture({ id: "inv-77" })} />);
    fireEvent.click(screen.getByRole("button", { name: /download pdf/i }));

    await waitFor(() => {
      expect(anchors).toHaveLength(1);
    });
    expect(anchors[0].download).toBe("invoice-inv-77.pdf");
  });

  it("ignores a second click while generation is still running", async () => {
    const anchors = installDownloadCapture();
    pdfHarness.defer = true;

    render(<InvoiceDownloadButton invoice={invoiceFixture()} />);
    const button = () => screen.getByRole("button", { name: /download pdf|generating/i });

    const settled = act(async () => {
      fireEvent.click(button());
      // Generation is parked on the deferred resolver; the label flips.
      await waitFor(() => {
        expect(screen.getByText(/Generating/)).toBeTruthy();
      });
      fireEvent.click(button()); // busy guard + disabled state
      for (const resolve of pdfHarness.resolvers.splice(0)) {
        resolve(new Blob(["%PDF-fake"], { type: "application/pdf" }));
      }
    });
    await settled;

    expect(anchors).toHaveLength(1);
    expect(screen.getByRole("button", { name: /download pdf/i }).textContent).toContain(
      "Download PDF",
    );
  });

  it("generation failure alerts instead of downloading", async () => {
    const alertSpy = vi.fn();
    Object.defineProperty(window, "alert", {
      value: alertSpy,
      configurable: true,
      writable: true,
    });
    installDownloadCapture();
    pdfHarness.failNext = true;

    render(<InvoiceDownloadButton invoice={invoiceFixture()} />);
    fireEvent.click(screen.getByRole("button", { name: /download pdf/i }));

    await waitFor(() => {
      expect(alertSpy).toHaveBeenCalledTimes(1);
    });
    expect(String(alertSpy.mock.calls[0][0])).toContain("render exploded");
    expect(
      screen.getByRole("button", { name: /download pdf/i }).textContent,
    ).toContain("Download PDF");
  });

  it("missing local_currency refuses to render and alerts", async () => {
    const alertSpy = vi.fn();
    Object.defineProperty(window, "alert", {
      value: alertSpy,
      configurable: true,
      writable: true,
    });
    installDownloadCapture();

    render(
      <InvoiceDownloadButton
        invoice={invoiceFixture({ local_currency: "" })}
      />,
    );
    fireEvent.click(screen.getByRole("button", { name: /download pdf/i }));

    await waitFor(() => {
      expect(alertSpy).toHaveBeenCalledTimes(1);
    });
    expect(String(alertSpy.mock.calls[0][0])).toContain("local_currency");
  });
});
