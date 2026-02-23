"use client";

import { useState } from "react";

import { Button } from "../../../components/ui/button";

type CodeBlockProps = {
  code: string;
  language?: string;
};

export function CodeBlock({ code, language }: CodeBlockProps) {
  const [copied, setCopied] = useState(false);

  async function handleCopy() {
    if (!navigator?.clipboard) {
      return;
    }
    await navigator.clipboard.writeText(code);
    setCopied(true);
    window.setTimeout(() => setCopied(false), 1000);
  }

  return (
    <div className="overflow-hidden rounded-lg border bg-slate-950 text-slate-100">
      <div className="flex items-center justify-between border-b border-slate-800 px-3 py-2 text-xs">
        <span className="font-mono uppercase tracking-wide text-slate-300">{language ?? "text"}</span>
        <Button type="button" variant="ghost" size="sm" className="h-7 text-slate-200 hover:bg-slate-800" onClick={handleCopy}>
          {copied ? "Copied" : "Copy code"}
        </Button>
      </div>
      <pre className="overflow-x-auto p-3 text-xs leading-relaxed sm:text-sm">
        <code>{code}</code>
      </pre>
    </div>
  );
}
