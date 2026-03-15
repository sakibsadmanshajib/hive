import { SendHorizontal } from "lucide-react";

import { Button } from "../../../components/ui/button";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "../../../components/ui/select";
import { Textarea } from "../../../components/ui/textarea";
import type { ChatModelOption } from "../use-chat-session";
import { useChatShortcuts } from "../hooks/use-chat-shortcuts";

type MessageComposerProps = {
  prompt: string;
  model: string;
  modelOptions: ChatModelOption[];
  guestMode: boolean;
  loading: boolean;
  onPromptChange: (value: string) => void;
  onModelChange: (value: string) => void;
  onSend: () => void;
};

export function MessageComposer({
  prompt,
  model,
  modelOptions,
  guestMode,
  loading,
  onPromptChange,
  onModelChange,
  onSend,
}: MessageComposerProps) {
  const canSend = prompt.trim().length > 0 && !loading && (!guestMode || model.trim().length > 0);
  const onKeyDown = useChatShortcuts({ canSend, onSend });

  return (
    <div className="space-y-3 rounded-3xl border border-white/80 bg-card/95 p-3 shadow-[0_20px_45px_-38px_rgba(15,23,42,0.95)] backdrop-blur sm:p-4">
      <div className="grid gap-3 sm:grid-cols-[170px_1fr]">
        <Select value={model} onValueChange={onModelChange}>
          <SelectTrigger aria-label="Model" className="bg-background/90">
            <SelectValue placeholder="Model" />
          </SelectTrigger>
          <SelectContent>
            {modelOptions.map((option) => (
              <SelectItem key={option.id} value={option.id}>
                <span className="inline-flex w-full items-center justify-between gap-3">
                  <span>{option.id}</span>
                  {option.locked ? (
                    <span className="rounded-full bg-amber-100 px-2 py-0.5 text-[10px] font-semibold uppercase tracking-wide text-amber-900">
                      Locked
                    </span>
                  ) : null}
                </span>
                {option.locked && option.lockReason ? (
                  <span className="mt-1 block text-xs text-muted-foreground">{option.lockReason}</span>
                ) : null}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
        <Textarea
          rows={3}
          value={prompt}
          onChange={(event) => onPromptChange(event.target.value)}
          onKeyDown={onKeyDown}
          placeholder="Ask something..."
          className="min-h-[96px] bg-background/90"
        />
      </div>
      <div className="flex items-center justify-between gap-2">
        <p className="text-xs font-medium text-muted-foreground">
          {guestMode ? "Guest mode only supports free models." : "Enter to send, Shift+Enter for newline"}
        </p>
        <Button type="button" disabled={!canSend} onClick={onSend}>
          <SendHorizontal className="h-4 w-4" />
          {loading ? "Sending..." : "Send"}
        </Button>
      </div>
    </div>
  );
}
