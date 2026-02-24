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
            ? "rounded-br-md bg-gradient-to-br from-slate-700 to-slate-600 text-slate-100"
            : "rounded-bl-md border border-slate-700 bg-slate-900 text-slate-100",
        )}
      >
        <p className={cn("mb-2 text-[10px] font-semibold uppercase tracking-[0.14em]", isUser ? "text-slate-300" : "text-slate-400")}>
          {speaker}
        </p>
        {isUser ? <p className="whitespace-pre-wrap leading-relaxed">{content}</p> : <MarkdownMessage content={content} />}
        <p className={cn("mt-2 text-[10px] uppercase tracking-wide", isUser ? "text-slate-300" : "text-slate-400")}>{timestamp ?? "just now"}</p>
      </div>
    </article>
  );
}
