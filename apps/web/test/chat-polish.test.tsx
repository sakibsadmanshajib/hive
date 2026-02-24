// @vitest-environment jsdom

import "@testing-library/jest-dom/vitest";
import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { MessageBubble } from "../src/features/chat/components/message-bubble";
import { MessageComposer } from "../src/features/chat/components/message-composer";
import { MessageList } from "../src/features/chat/components/message-list";

describe("chat polish", () => {
  it("shows clear role labels in message bubbles", () => {
    render(
      <>
        <MessageBubble role="assistant" content="Hello there" />
        <MessageBubble role="user" content="Hi" />
      </>,
    );

    expect(screen.getByText("Assistant")).toBeInTheDocument();
    expect(screen.getByText("You")).toBeInTheDocument();
  });

  it("shows keyboard hint text in composer", () => {
    render(
      <MessageComposer
        prompt=""
        model="fast-chat"
        loading={false}
        onPromptChange={() => {}}
        onModelChange={() => {}}
        onSend={() => {}}
      />,
    );

    expect(screen.getByText(/enter to send/i)).toBeInTheDocument();
    expect(screen.getByText(/shift\+enter for newline/i)).toBeInTheDocument();
  });

  it("renders persisted message timestamp from message metadata", () => {
    render(
      <MessageList
        messages={[{ role: "assistant", content: "Hi", createdAt: "2000-01-01T10:00:00" }]}
        loading={false}
        errorMessage={null}
      />,
    );

    expect(screen.getByText(/10\D*00/i)).toBeInTheDocument();
  });
});
