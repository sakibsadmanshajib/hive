export function TypingIndicator() {
  return (
    <div className="flex items-center gap-1 rounded-2xl rounded-bl-md border bg-card px-3 py-2 text-muted-foreground">
      <span className="sr-only">Assistant is typing</span>
      <span className="h-2 w-2 animate-pulse rounded-full bg-muted-foreground/70" />
      <span className="h-2 w-2 animate-pulse rounded-full bg-muted-foreground/70 [animation-delay:120ms]" />
      <span className="h-2 w-2 animate-pulse rounded-full bg-muted-foreground/70 [animation-delay:240ms]" />
    </div>
  );
}
