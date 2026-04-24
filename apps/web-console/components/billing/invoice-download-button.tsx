"use client";

import { createElement as h, useState } from "react";
import type { Invoice } from "@/lib/control-plane/client";

interface InvoiceDownloadButtonProps {
  invoice: Invoice;
}

function formatDate(isoString: string): string {
  try {
    return new Date(isoString).toLocaleDateString("en-US", {
      year: "numeric",
      month: "long",
      day: "numeric",
    });
  } catch {
    return isoString;
  }
}

function formatAmount(amountCents: number, currency: string): string {
  return new Intl.NumberFormat("en-US", {
    style: "currency",
    currency,
    minimumFractionDigits: 2,
  }).format(amountCents / 100);
}

export function InvoiceDownloadButton({ invoice }: InvoiceDownloadButtonProps) {
  const [busy, setBusy] = useState(false);

  async function handleClick() {
    if (busy) return;
    setBusy(true);
    try {
      const { pdf, Document, Page, Text, View, StyleSheet } = await import(
        "@react-pdf/renderer"
      );

      const styles = StyleSheet.create({
        page: { fontFamily: "Helvetica", fontSize: 11, padding: 40, color: "#1f2937" },
        header: { marginBottom: 24, borderBottomWidth: 1, borderBottomColor: "#e5e7eb", paddingBottom: 12 },
        brand: { fontSize: 22, fontFamily: "Helvetica-Bold", color: "#111827" },
        invoiceTitle: { fontSize: 12, color: "#6b7280", marginTop: 4 },
        metaRow: { flexDirection: "row", marginBottom: 4 },
        metaLabel: { width: 130, fontFamily: "Helvetica-Bold", color: "#374151" },
        metaValue: { flex: 1 },
        section: { marginTop: 18, marginBottom: 8, fontFamily: "Helvetica-Bold", fontSize: 12, color: "#111827" },
        tableHeader: {
          flexDirection: "row",
          backgroundColor: "#f9fafb",
          borderTopWidth: 1,
          borderBottomWidth: 1,
          borderColor: "#e5e7eb",
          padding: 6,
          fontFamily: "Helvetica-Bold",
          fontSize: 10,
        },
        tableRow: {
          flexDirection: "row",
          borderBottomWidth: 1,
          borderBottomColor: "#f3f4f6",
          padding: 6,
        },
        col1: { flex: 3 },
        col2: { flex: 1, textAlign: "right" },
        col3: { flex: 1, textAlign: "right" },
        totalRow: {
          flexDirection: "row",
          borderTopWidth: 1,
          borderTopColor: "#e5e7eb",
          padding: 6,
          fontFamily: "Helvetica-Bold",
          fontSize: 11,
          marginTop: 4,
        },
        footer: {
          marginTop: 32,
          fontSize: 9,
          color: "#6b7280",
          borderTopWidth: 1,
          borderTopColor: "#e5e7eb",
          paddingTop: 8,
        },
      });

      const currency = invoice.local_currency || "USD";
      const amountDisplay = formatAmount(invoice.amount_local, currency);

      const lineItems = Array.isArray(invoice.line_items) ? invoice.line_items : [];
      const extraRows = lineItems.map((item, idx) => {
        const desc = typeof item.description === "string" ? item.description : `Line item ${idx + 1}`;
        const credits = typeof item.credits === "number" ? item.credits.toLocaleString() : "";
        const amount =
          typeof item.amount_local === "number"
            ? formatAmount(item.amount_local, currency)
            : "";
        return h(
          View,
          { key: `extra-${idx}`, style: styles.tableRow },
          h(Text, { style: styles.col1 }, desc),
          h(Text, { style: styles.col2 }, credits),
          h(Text, { style: styles.col3 }, amount),
        );
      });

      const extraCreditsSum = lineItems.reduce(
        (sum, item) => sum + (typeof item.credits === "number" ? item.credits : 0),
        0,
      );
      const extraAmountSum = lineItems.reduce(
        (sum, item) => sum + (typeof item.amount_local === "number" ? item.amount_local : 0),
        0,
      );
      const totalCredits = invoice.credits + extraCreditsSum;
      const totalAmountLocal = invoice.amount_local + extraAmountSum;
      const totalAmountDisplay = formatAmount(totalAmountLocal, currency);

      const doc = h(
        Document,
        {},
        h(
          Page,
          { size: "A4", style: styles.page },
          h(
            View,
            { style: styles.header },
            h(Text, { style: styles.brand }, "Hive"),
            h(Text, { style: styles.invoiceTitle }, "Invoice"),
          ),
          h(
            View,
            { style: styles.metaRow },
            h(Text, { style: styles.metaLabel }, "Invoice Number:"),
            h(Text, { style: styles.metaValue }, invoice.invoice_number || invoice.id),
          ),
          h(
            View,
            { style: styles.metaRow },
            h(Text, { style: styles.metaLabel }, "Date:"),
            h(Text, { style: styles.metaValue }, formatDate(invoice.created_at)),
          ),
          h(
            View,
            { style: styles.metaRow },
            h(Text, { style: styles.metaLabel }, "Payment Rail:"),
            h(Text, { style: styles.metaValue }, invoice.rail),
          ),
          h(
            View,
            { style: styles.metaRow },
            h(Text, { style: styles.metaLabel }, "Tax Treatment:"),
            h(Text, { style: styles.metaValue }, invoice.tax_treatment || "N/A"),
          ),
          h(
            View,
            { style: styles.metaRow },
            h(Text, { style: styles.metaLabel }, "Status:"),
            h(Text, { style: styles.metaValue }, invoice.status),
          ),
          h(Text, { style: styles.section }, "Line Items"),
          h(
            View,
            { style: styles.tableHeader },
            h(Text, { style: styles.col1 }, "Description"),
            h(Text, { style: styles.col2 }, "Credits"),
            h(Text, { style: styles.col3 }, "Amount"),
          ),
          h(
            View,
            { style: styles.tableRow },
            h(
              Text,
              { style: styles.col1 },
              `Hive Credits — ${invoice.credits.toLocaleString()} credits`,
            ),
            h(Text, { style: styles.col2 }, invoice.credits.toLocaleString()),
            h(Text, { style: styles.col3 }, amountDisplay),
          ),
          ...extraRows,
          h(
            View,
            { style: styles.totalRow },
            h(Text, { style: styles.col1 }, "Total"),
            h(Text, { style: styles.col2 }, totalCredits.toLocaleString()),
            h(Text, { style: styles.col3 }, totalAmountDisplay),
          ),
          h(
            Text,
            { style: styles.footer },
            "Hive — This invoice is generated automatically.",
          ),
        ),
      );

      const blob = await pdf(doc).toBlob();
      const url = URL.createObjectURL(blob);
      const a = document.createElement("a");
      a.href = url;
      a.download = `invoice-${invoice.id}.pdf`;
      document.body.appendChild(a);
      a.click();
      document.body.removeChild(a);
      // Defer revoke so the browser has a tick to start the download before the blob URL is invalidated.
      setTimeout(() => URL.revokeObjectURL(url), 0);
    } catch (err) {
      // eslint-disable-next-line no-alert
      alert(`Failed to generate PDF: ${err instanceof Error ? err.message : String(err)}`);
    } finally {
      setBusy(false);
    }
  }

  return (
    <button
      type="button"
      onClick={handleClick}
      disabled={busy}
      style={{
        color: "#1d4ed8",
        background: "none",
        border: "none",
        padding: 0,
        cursor: busy ? "wait" : "pointer",
        textDecoration: "none",
        fontSize: "0.875rem",
        opacity: busy ? 0.6 : 1,
      }}
    >
      {busy ? "Generating…" : "Download PDF"}
    </button>
  );
}
