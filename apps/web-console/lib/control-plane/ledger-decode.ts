// Client-safe ledger row decoding.
//
// Lives outside client.ts because that module is server-only (next/headers);
// the request-log table is a browser component and must not pull it into the
// client graph. Same validation discipline as the decoders in client.ts:
// every field is read with a typeof check, malformed rows are skipped, and
// nothing unvalidated reaches the UI. The LedgerEntry shape itself is
// imported as a type only, so no runtime dependency on the server module.

import type { LedgerEntry } from "@/lib/control-plane/client";

interface JsonObject {
  [key: string]: unknown;
}

function readString(source: JsonObject, key: string): string | null {
  const value = source[key];
  return typeof value === "string" ? value : null;
}

function readNumber(source: JsonObject, key: string): number | null {
  const value = source[key];
  return typeof value === "number" ? value : null;
}

function decodeLedgerEntry(value: unknown): LedgerEntry | null {
  if (typeof value !== "object" || value === null || Array.isArray(value)) {
    return null;
  }
  const source = value as JsonObject;

  const id = readString(source, "id");
  const entryType = readString(source, "entry_type");
  const creditsDelta = readNumber(source, "credits_delta");
  const createdAt = readString(source, "created_at");

  if (!id || !entryType || creditsDelta === null || !createdAt) {
    return null;
  }

  return {
    id,
    entry_type: entryType,
    credits_delta: creditsDelta,
    idempotency_key: "",
    request_id: "",
    metadata: {},
    created_at: createdAt,
  };
}

// parseLedgerEntriesText decodes a raw JSON response body into validated
// LedgerEntry rows. Malformed rows are skipped; an unparseable or non-object
// body yields an empty list rather than throwing, so the request-log detail
// view degrades to "no ledger activity" instead of an error surface.
export function parseLedgerEntriesText(text: string): LedgerEntry[] {
  let payload: unknown = null;
  try {
    payload = JSON.parse(text);
  } catch {
    return [];
  }
  if (typeof payload !== "object" || payload === null || Array.isArray(payload)) {
    return [];
  }
  const raw = (payload as JsonObject)["entries"];
  if (!Array.isArray(raw)) {
    return [];
  }
  const entries: LedgerEntry[] = [];
  for (const item of raw) {
    const decoded = decodeLedgerEntry(item);
    if (decoded) {
      entries.push(decoded);
    }
  }
  return entries;
}
