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

function formatTimestamp(timestamp: string): string {
  const date = new Date(timestamp);
  return Number.isNaN(date.getTime()) ? "just now" : date.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" });
}

export function MessageList({ messages, loading, errorMessage }: MessageListProps) {
  return (
    <ScrollArea className="h-[50vh] rounded-3xl border border-white/80 bg-[linear-gradient(180deg,rgba(255,255,255,0.92)_0%,rgba(247,250,252,0.9)_54%,rgba(255,251,243,0.82)_100%)] p-3 shadow-[0_20px_45px_-38px_rgba(15,23,42,0.9)] backdrop-blur sm:h-[58vh] sm:p-5">
      <div className="space-y-4">
        {messages.length === 0 ? <p className="text-sm text-muted-foreground">Start a new chat to see messages.</p> : null}
        {messages.map((message) => (
          <MessageBubble
            key={`${message.createdAt}-${message.role}-${message.content.slice(0, 24)}`}
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
