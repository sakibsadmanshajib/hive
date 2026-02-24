import { ScrollArea } from "../../../components/ui/scroll-area";
import { Skeleton } from "../../../components/ui/skeleton";
import { MessageBubble } from "./message-bubble";
import { TypingIndicator } from "./typing-indicator";

type MessageItem = {
  role: "user" | "assistant";
  content: string;
  createdAt: string;
};

type MessageListProps = {
  messages: MessageItem[];
  loading: boolean;
  errorMessage: string | null;
};

export function MessageList({ messages, loading, errorMessage }: MessageListProps) {
  function formatTimestamp(timestamp: string): string {
    const date = new Date(timestamp);
    return Number.isNaN(date.getTime())
      ? "just now"
      : date.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" });
  }

  return (
    <ScrollArea className="h-[56vh] rounded-2xl border border-slate-800 bg-slate-950/85 p-3 shadow-sm sm:h-[62vh] sm:p-4">
      <div className="space-y-4">
        {messages.length === 0 ? <p className="text-sm text-slate-400">Start a new chat to see messages.</p> : null}
        {messages.map((message, index) => (
          <MessageBubble
            key={`${message.role}-${index}`}
            role={message.role}
            content={message.content}
            timestamp={formatTimestamp(message.createdAt)}
          />
        ))}
        {loading ? (
          <div className="space-y-2">
            <TypingIndicator />
            <Skeleton className="h-4 w-40" />
          </div>
        ) : null}
        {errorMessage ? <p className="rounded-lg border border-destructive/30 bg-destructive/10 px-3 py-2 text-sm text-destructive">{errorMessage}</p> : null}
      </div>
    </ScrollArea>
  );
}
