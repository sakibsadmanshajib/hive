import { useMemo } from "react";
import type { KeyboardEvent } from "react";

type ShortcutOptions = {
  canSend: boolean;
  onSend: () => void;
};

export function createComposerShortcutHandler({ canSend, onSend }: ShortcutOptions) {
  return (event: KeyboardEvent<HTMLTextAreaElement>) => {
    if (event.key !== "Enter") {
      return;
    }
    if (event.shiftKey) {
      return;
    }

    event.preventDefault();
    if (!canSend) {
      return;
    }

    onSend();
  };
}

export function useChatShortcuts(options: ShortcutOptions) {
  return useMemo(() => createComposerShortcutHandler(options), [options]);
}
