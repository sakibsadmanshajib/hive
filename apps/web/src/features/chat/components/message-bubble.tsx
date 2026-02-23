import { cn } from "../../../lib/utils";
import { MarkdownMessage } from "./markdown-message";

type MessageBubbleProps = {
  role: "user" | "assistant";
  content: string;
  timestamp?: string;
};

export function MessageBubble({ role, content, timestamp }: MessageBubbleProps) {
  const isUser = role === "user";
  const speaker = isUser ? "You" : "Assistant";

  return (
    <article className={cn("flex w-full", isUser ? "justify-end" : "justify-start")}>
      <div
        className={cn(
          "max-w-[88%] rounded-2xl px-4 py-3 text-sm shadow-sm transition-all duration-200 hover:-translate-y-0.5 hover:shadow-md sm:max-w-[75%]",
          isUser
            ? "rounded-br-md bg-gradient-to-br from-primary to-primary/85 text-primary-foreground"
            : "rounded-bl-md border bg-card/95 text-card-foreground",
        )}
      >
        <p className={cn("mb-2 text-[10px] font-semibold uppercase tracking-[0.14em]", isUser ? "text-primary-foreground/75" : "text-muted-foreground")}>
          {speaker}
        </p>
        {isUser ? <p className="whitespace-pre-wrap leading-relaxed">{content}</p> : <MarkdownMessage content={content} />}
        <p className={cn("mt-2 text-[10px] uppercase tracking-wide", isUser ? "text-primary-foreground/70" : "text-muted-foreground")}>{timestamp ?? "just now"}</p>
      </div>
    </article>
  );
}
