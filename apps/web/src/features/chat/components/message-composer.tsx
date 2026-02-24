import { SendHorizontal } from "lucide-react";

import { Button } from "../../../components/ui/button";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "../../../components/ui/select";
import { Textarea } from "../../../components/ui/textarea";
import { useChatShortcuts } from "../hooks/use-chat-shortcuts";

type MessageComposerProps = {
  prompt: string;
  model: string;
  loading: boolean;
  onPromptChange: (value: string) => void;
  onModelChange: (value: string) => void;
  onSend: () => void;
};

export function MessageComposer({ prompt, model, loading, onPromptChange, onModelChange, onSend }: MessageComposerProps) {
  const canSend = prompt.trim().length > 0 && !loading;
  const onKeyDown = useChatShortcuts({ canSend, onSend });

  return (
    <div className="space-y-3 rounded-2xl border border-slate-800 bg-slate-950/95 p-3 shadow-sm backdrop-blur sm:p-4">
      <div className="grid gap-3 sm:grid-cols-[170px_1fr]">
        <Select value={model} onValueChange={onModelChange}>
          <SelectTrigger aria-label="Model">
            <SelectValue placeholder="Model" />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="fast-chat">fast-chat</SelectItem>
            <SelectItem value="smart-reasoning">smart-reasoning</SelectItem>
          </SelectContent>
        </Select>
        <Textarea
          rows={3}
          value={prompt}
          onChange={(event) => onPromptChange(event.target.value)}
          onKeyDown={onKeyDown}
          placeholder="Ask something..."
          className="min-h-[88px]"
        />
      </div>
      <div className="flex items-center justify-between gap-2">
        <p className="text-xs text-slate-400">Enter to send, Shift+Enter for newline</p>
        <Button type="button" disabled={!canSend} onClick={onSend} className="bg-slate-700 text-slate-100 hover:bg-slate-600">
          <SendHorizontal className="h-4 w-4" />
          {loading ? "Sending..." : "Send"}
        </Button>
      </div>
    </div>
  );
}
