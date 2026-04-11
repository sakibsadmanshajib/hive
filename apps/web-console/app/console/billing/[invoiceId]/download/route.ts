import { getInvoice } from "@/lib/control-plane/client";
import { renderToBuffer, Document, Page, Text, View, StyleSheet } from "@react-pdf/renderer";
import type { Invoice } from "@/lib/control-plane/client";
import React from "react";

const styles = StyleSheet.create({
  page: {
    fontFamily: "Helvetica",
    fontSize: 11,
    padding: 40,
    color: "#1f2937",
  },
  header: {
    marginBottom: 24,
    borderBottomWidth: 1,
    borderBottomColor: "#e5e7eb",
    paddingBottom: 12,
  },
  brand: {
    fontSize: 20,
    fontFamily: "Helvetica-Bold",
    color: "#1d4ed8",
    marginBottom: 4,
  },
  invoiceTitle: {
    fontSize: 14,
    fontFamily: "Helvetica-Bold",
    marginBottom: 2,
  },
  metaRow: {
    flexDirection: "row",
    marginBottom: 4,
  },
  metaLabel: {
    width: 120,
    color: "#4b5563",
  },
  metaValue: {
    flex: 1,
  },
  section: {
    marginTop: 20,
    marginBottom: 8,
    fontSize: 12,
    fontFamily: "Helvetica-Bold",
    borderBottomWidth: 1,
    borderBottomColor: "#e5e7eb",
    paddingBottom: 4,
  },
  tableHeader: {
    flexDirection: "row",
    backgroundColor: "#f9fafb",
    padding: 4,
    fontFamily: "Helvetica-Bold",
    fontSize: 10,
    color: "#4b5563",
  },
  tableRow: {
    flexDirection: "row",
    borderBottomWidth: 1,
    borderBottomColor: "#f3f4f6",
    padding: 4,
    fontSize: 10,
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

function buildInvoiceDocument(invoice: Invoice): React.ReactElement<React.ComponentProps<typeof Document>> {
  const amountDisplay = formatAmount(invoice.amount_local, invoice.local_currency || "USD");

  const extraRows = invoice.line_items.map((item, idx) => {
    const desc = typeof item.description === "string" ? item.description : `Line item ${idx + 1}`;
    const credits =
      typeof item.credits === "number" ? item.credits.toLocaleString() : "";
    const amount =
      typeof item.amount_local === "number" && typeof item.currency === "string"
        ? formatAmount(item.amount_local, item.currency)
        : "";
    return React.createElement(
      View,
      { key: String(idx), style: styles.tableRow },
      React.createElement(Text, { style: styles.col1 }, desc),
      React.createElement(Text, { style: styles.col2 }, credits),
      React.createElement(Text, { style: styles.col3 }, amount),
    );
  });

  return React.createElement(
    Document,
    {} as React.ComponentProps<typeof Document>,
    React.createElement(
      Page,
      { size: "A4", style: styles.page },
      // Header
      React.createElement(
        View,
        { style: styles.header },
        React.createElement(Text, { style: styles.brand }, "Hive"),
        React.createElement(Text, { style: styles.invoiceTitle }, "Invoice"),
      ),
      // Invoice metadata
      React.createElement(
        View,
        { style: styles.metaRow },
        React.createElement(Text, { style: styles.metaLabel }, "Invoice Number:"),
        React.createElement(
          Text,
          { style: styles.metaValue },
          invoice.invoice_number || invoice.id,
        ),
      ),
      React.createElement(
        View,
        { style: styles.metaRow },
        React.createElement(Text, { style: styles.metaLabel }, "Date:"),
        React.createElement(Text, { style: styles.metaValue }, formatDate(invoice.created_at)),
      ),
      React.createElement(
        View,
        { style: styles.metaRow },
        React.createElement(Text, { style: styles.metaLabel }, "Payment Rail:"),
        React.createElement(Text, { style: styles.metaValue }, invoice.rail),
      ),
      React.createElement(
        View,
        { style: styles.metaRow },
        React.createElement(Text, { style: styles.metaLabel }, "Tax Treatment:"),
        React.createElement(Text, { style: styles.metaValue }, invoice.tax_treatment || "N/A"),
      ),
      React.createElement(
        View,
        { style: styles.metaRow },
        React.createElement(Text, { style: styles.metaLabel }, "Status:"),
        React.createElement(Text, { style: styles.metaValue }, invoice.status),
      ),
      // Line items section
      React.createElement(Text, { style: styles.section }, "Line Items"),
      React.createElement(
        View,
        { style: styles.tableHeader },
        React.createElement(Text, { style: styles.col1 }, "Description"),
        React.createElement(Text, { style: styles.col2 }, "Credits"),
        React.createElement(Text, { style: styles.col3 }, "Amount"),
      ),
      // Credits purchase line item
      React.createElement(
        View,
        { style: styles.tableRow },
        React.createElement(
          Text,
          { style: styles.col1 },
          `Hive Credits — ${invoice.credits.toLocaleString()} credits`,
        ),
        React.createElement(Text, { style: styles.col2 }, invoice.credits.toLocaleString()),
        React.createElement(Text, { style: styles.col3 }, amountDisplay),
      ),
      // Additional line items
      ...extraRows,
      // Total row
      React.createElement(
        View,
        { style: styles.totalRow },
        React.createElement(Text, { style: styles.col1 }, "Total"),
        React.createElement(Text, { style: styles.col2 }, invoice.credits.toLocaleString()),
        React.createElement(Text, { style: styles.col3 }, amountDisplay),
      ),
      // Footer
      React.createElement(
        Text,
        { style: styles.footer },
        "Hive — This invoice is generated automatically. For billing inquiries, contact support.",
      ),
    ),
  );
}

export async function GET(
  _request: Request,
  { params }: { params: Promise<{ invoiceId: string }> },
) {
  const { invoiceId } = await params;
  const invoice = await getInvoice(invoiceId);
  const doc = buildInvoiceDocument(invoice);
  const buffer = await renderToBuffer(doc);
  const uint8 = new Uint8Array(buffer);

  return new Response(uint8, {
    headers: {
      "Content-Type": "application/pdf",
      "Content-Disposition": `attachment; filename=invoice-${invoiceId}.pdf`,
    },
  });
}
