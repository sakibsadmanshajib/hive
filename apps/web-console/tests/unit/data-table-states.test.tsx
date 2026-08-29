import { describe, expect, it } from "vitest";
import { render, screen } from "@testing-library/react";

import { DataTable } from "@/components/ui/data-table";

interface Row {
  id: string;
  name: string;
}

const columns = [
  { key: "name", header: "Name", cell: (row: Row) => row.name },
];

const rowKey = (row: Row) => row.id;

describe("DataTable pending and empty states", () => {
  it("shows a pending row while loading rather than the empty state", () => {
    render(
      <DataTable rows={[]} columns={columns} rowKey={rowKey} loading />,
    );

    expect(screen.getByText("Loading...")).toBeTruthy();
    expect(screen.queryByText("No records yet.")).toBeNull();
  });

  it("shows the empty state once loading has finished with no rows", () => {
    render(<DataTable rows={[]} columns={columns} rowKey={rowKey} />);

    expect(screen.getByText("No records yet.")).toBeTruthy();
    expect(screen.queryByText("Loading...")).toBeNull();
  });

  it("marks the table body busy only while loading", () => {
    const { container, rerender } = render(
      <DataTable rows={[]} columns={columns} rowKey={rowKey} loading />,
    );
    expect(container.querySelector("tbody")?.getAttribute("aria-busy")).toBe(
      "true",
    );

    rerender(<DataTable rows={[]} columns={columns} rowKey={rowKey} />);
    expect(container.querySelector("tbody")?.getAttribute("aria-busy")).toBe(
      "false",
    );
  });

  it("keeps rows on screen while a refresh is in flight", () => {
    render(
      <DataTable
        rows={[{ id: "r1", name: "Existing row" }]}
        columns={columns}
        rowKey={rowKey}
        loading
      />,
    );

    expect(screen.getByText("Existing row")).toBeTruthy();
    expect(screen.queryByText("Loading...")).toBeNull();
  });

  it("scrolls a wide table horizontally instead of clipping it", () => {
    const { container } = render(
      <DataTable rows={[]} columns={columns} rowKey={rowKey} />,
    );

    const wrapper = container.firstElementChild;
    expect(wrapper?.className).toContain("overflow-x-auto");
    expect(wrapper?.className).not.toContain("overflow-hidden");
    // Not focusable on purpose: see the comment in data-table.tsx and
    // issue #1385. Pinned so the next attempt at the keyboard fix is a
    // deliberate change to this assertion rather than a silent one that
    // fails the interaction coverage gate on three routes.
    expect(wrapper?.hasAttribute("tabindex")).toBe(false);
  });
});
