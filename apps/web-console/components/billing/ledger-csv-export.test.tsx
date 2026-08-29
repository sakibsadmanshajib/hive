/**
 * Behavioral tests for LedgerCsvExport: the export button must produce a
 * real CSV blob download named ledger-export.csv and stay inert when there
 * is nothing to export.
 */
import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";

import { LedgerCsvExport } from "./ledger-csv-export";
import type { LedgerEntry } from "@/lib/control-plane/client";

function entryFixture(overrides: Partial<LedgerEntry> = {}): LedgerEntry {
  return {
    id: "le-1",
    entry_type: "usage",
    credits_delta: -1_250,
    idempotency_key: "idem-abc-123",
    request_id: "req-1",
    metadata: {},
    created_at: "2026-08-20T10:00:00Z",
    ...overrides,
  };
}

interface CapturedAnchor {
  download: string | null;
  href: string;
}

// jsdom Blob predates the .text() convenience; read through FileReader.
function readBlobText(blob: Blob): Promise<string> {
  return new Promise((resolve, reject) => {
    const reader = new FileReader();
    reader.onload = () => resolve(String(reader.result));
    reader.onerror = () => reject(reader.error);
    reader.readAsText(blob);
  });
}

afterEach(() => {
  cleanup();
  // Remove the static URL method stubs installed by Object.defineProperty.
  Reflect.deleteProperty(URL, "createObjectURL");
  Reflect.deleteProperty(URL, "revokeObjectURL");
  vi.restoreAllMocks();
});

function installDownloadCapture(): {
  anchors: CapturedAnchor[];
  blobs: Blob[];
} {
  const anchors: CapturedAnchor[] = [];
  const blobs: Blob[] = [];
  const clickSpy = vi
    .spyOn(HTMLAnchorElement.prototype, "click")
    .mockImplementation(function mockClick(this: HTMLAnchorElement) {
      anchors.push({
        download: this.getAttribute("download"),
        href: this.href,
      });
    });
  void clickSpy;
  let urlCounter = 0;
  Object.defineProperty(URL, "createObjectURL", {
    value: (blob: Blob) => {
      blobs.push(blob);
      urlCounter += 1;
      return `blob:mock-${urlCounter}`;
    },
    configurable: true,
    writable: true,
  });
  Object.defineProperty(URL, "revokeObjectURL", {
    value: () => {},
    configurable: true,
    writable: true,
  });
  return { anchors, blobs };
}

describe("LedgerCsvExport", () => {
  it("is disabled when there is nothing to export", () => {
    render(<LedgerCsvExport entries={[]} />);
    const button = screen.getByRole("button", { name: /export csv/i });
    expect(button instanceof HTMLButtonElement && button.disabled).toBe(true);
  });

  it("export triggers a ledger-export.csv download whose content is the rendered rows", async () => {
    const { anchors, blobs } = installDownloadCapture();
    render(
      <LedgerCsvExport
        entries={[
          entryFixture(),
          entryFixture({
            id: "le-2",
            entry_type: "topup",
            credits_delta: 50_000_000,
            idempotency_key: "idem,x-with-commas",
            created_at: "2026-08-21T09:30:00Z",
          }),
        ]}
      />,
    );

    fireEvent.click(screen.getByRole("button", { name: /export csv/i }));

    expect(anchors).toHaveLength(1);
    expect(anchors[0].download).toBe("ledger-export.csv");
    expect(blobs).toHaveLength(1);
    expect(blobs[0].type).toBe("text/csv");
    const csv = await readBlobText(blobs[0]);
    expect(csv.split("\n")).toEqual([
      "date,type,credits_delta,idempotency_key",
      "2026-08-20T10:00:00Z,usage,-1250,idem-abc-123",
      // A comma is now quoted per RFC 4180 rather than deleted from the
      // value: the export exists to be reconciled against, so a mangled
      // idempotency key is worse than a quoted one. The negative delta is
      // left unprefixed on purpose so the column still sums (issue #1401).
      '2026-08-21T09:30:00Z,topup,50000000,"idem,x-with-commas"',
    ]);
  });

  it("neutralises a formula-leading idempotency key so a spreadsheet shows text", async () => {
    const { blobs } = installDownloadCapture();
    render(
      <LedgerCsvExport
        entries={[
          entryFixture({ idempotency_key: '=HYPERLINK("http://qa.invalid")' }),
        ]}
      />,
    );

    fireEvent.click(screen.getByRole("button", { name: /export csv/i }));

    const csv = await readBlobText(blobs[0]);
    const cell = csv.split("\n")[1].split(",").slice(3).join(",");
    expect(cell.startsWith('"\'=')).toBe(true);
  });

  it("revokes the object URL after triggering the click", async () => {
    const revokeSpy = vi.fn();
    Object.defineProperty(URL, "createObjectURL", {
      value: () => "blob:mock-revoke",
      configurable: true,
      writable: true,
    });
    Object.defineProperty(URL, "revokeObjectURL", {
      value: revokeSpy,
      configurable: true,
      writable: true,
    });
    vi.spyOn(HTMLAnchorElement.prototype, "click").mockImplementation(() => {});

    render(<LedgerCsvExport entries={[entryFixture()]} />);
    fireEvent.click(screen.getByRole("button", { name: /export csv/i }));

    expect(revokeSpy).toHaveBeenCalledWith("blob:mock-revoke");
  });
});
