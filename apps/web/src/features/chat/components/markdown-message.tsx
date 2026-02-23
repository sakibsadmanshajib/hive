import ReactMarkdown from "react-markdown";
import remarkGfm from "remark-gfm";

import { CodeBlock } from "./code-block";

type MarkdownMessageProps = {
  content: string;
};

export function MarkdownMessage({ content }: MarkdownMessageProps) {
  return (
    <div className="prose prose-sm max-w-none break-words text-current prose-pre:m-0 prose-code:text-current">
      <ReactMarkdown
        remarkPlugins={[remarkGfm]}
        components={{
          code({ children, className, ...props }) {
            const match = /language-(\w+)/.exec(className ?? "");
            const code = String(children).replace(/\n$/, "");

            if (!className) {
              return (
                <code className="rounded bg-foreground/10 px-1 py-0.5 text-[0.875em]" {...props}>
                  {children}
                </code>
              );
            }

            return <CodeBlock code={code} language={match?.[1]} />;
          },
        }}
      >
        {content}
      </ReactMarkdown>
    </div>
  );
}
