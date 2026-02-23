import { describe, expect, it, vi } from "vitest";

import { createComposerShortcutHandler } from "../src/features/chat/hooks/use-chat-shortcuts";

function createEvent(overrides: { key?: string; shiftKey?: boolean } = {}) {
  const preventDefault = vi.fn();
  return {
    key: overrides.key ?? "Enter",
    shiftKey: overrides.shiftKey ?? false,
    preventDefault,
  };
}

describe("createComposerShortcutHandler", () => {
  it("sends message on Enter", () => {
    const onSend = vi.fn();
    const event = createEvent({ key: "Enter" });
    const handleKeyDown = createComposerShortcutHandler({
      canSend: true,
      onSend,
    });

    handleKeyDown(event as never);

    expect(event.preventDefault).toHaveBeenCalledTimes(1);
    expect(onSend).toHaveBeenCalledTimes(1);
  });

  it("does not send on Shift+Enter", () => {
    const onSend = vi.fn();
    const event = createEvent({ key: "Enter", shiftKey: true });
    const handleKeyDown = createComposerShortcutHandler({
      canSend: true,
      onSend,
    });

    handleKeyDown(event as never);

    expect(event.preventDefault).not.toHaveBeenCalled();
    expect(onSend).not.toHaveBeenCalled();
  });

  it("ignores Enter when message cannot be sent", () => {
    const onSend = vi.fn();
    const event = createEvent({ key: "Enter" });
    const handleKeyDown = createComposerShortcutHandler({
      canSend: false,
      onSend,
    });

    handleKeyDown(event as never);

    expect(event.preventDefault).toHaveBeenCalledTimes(1);
    expect(onSend).not.toHaveBeenCalled();
  });
});
