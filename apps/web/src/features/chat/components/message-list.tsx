import { ScrollArea } from "../../../components/ui/scroll-area";
import { Skeleton } from "../../../components/ui/skeleton";
import { MessageBubble } from "./message-bubble";
import { TypingIndicator } from "./typing-indicator";

type MessageItem = {
  role: "user" | "assistant";
  content: string;
};

type MessageListProps = {
  messages: MessageItem[];
  loading: boolean;
  errorMessage: string | null;
};

export function MessageList({ messages, loading, errorMessage }: MessageListProps) {
  return (
    <ScrollArea className="h-[50vh] rounded-2xl border bg-[linear-gradient(180deg,rgba(255,255,255,0.82)_0%,rgba(248,250,252,0.88)_100%)] p-3 shadow-sm sm:h-[58vh] sm:p-4">
      <div className="space-y-4">
        {messages.length === 0 ? <p className="text-sm text-muted-foreground">Start a new chat to see messages.</p> : null}
        {messages.map((message, index) => (
          <MessageBubble
            key={`${message.role}-${index}`}
            role={message.role}
            content={message.content}
            timestamp={new Date().toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" })}
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
