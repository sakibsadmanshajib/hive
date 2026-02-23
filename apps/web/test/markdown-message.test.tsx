// @vitest-environment jsdom

import "@testing-library/jest-dom/vitest";
import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { MarkdownMessage } from "../src/features/chat/components/markdown-message";

describe("MarkdownMessage", () => {
  it("renders fenced code blocks", () => {
    render(<MarkdownMessage content={"```ts\nconst x = 1\n```"} />);

    expect(screen.getByText("const x = 1")).toBeInTheDocument();
  });

  it("shows copy button for code blocks", () => {
    render(<MarkdownMessage content={"```ts\nconst x = 1\n```"} />);

    expect(screen.getAllByRole("button", { name: /copy code/i }).length).toBeGreaterThan(0);
  });
});
