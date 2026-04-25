"use client";

import { useRouter } from "next/navigation";
import type { ChangeEvent } from "react";

import { Field } from "@/components/ui/input";
import { cn } from "@/lib/cn";
import { TimeWindowPicker } from "./time-window-picker";

interface AnalyticsControlsProps {
  currentGroupBy: string;
  currentWindow: string;
  activeTab: string;
}

const SELECT_CLASSES = cn(
  "flex h-9 w-full rounded-md border border-[var(--color-border)]",
  "bg-[var(--color-surface)] px-3 text-sm text-[var(--color-ink)]",
  "transition-[border,box-shadow] duration-[var(--duration-fast)]",
  "ease-[var(--ease-out-expo)]",
  "focus-visible:outline-none focus-visible:border-[var(--color-accent)]",
  "focus-visible:ring-4 focus-visible:ring-[var(--color-accent-soft)]",
);

export function AnalyticsControls({
  currentGroupBy,
  currentWindow,
  activeTab,
}: AnalyticsControlsProps) {
  const router = useRouter();

  function buildUrl(overrides: { group_by?: string; window?: string }) {
    const params = new URLSearchParams();
    params.set("tab", activeTab);
    params.set("group_by", overrides.group_by ?? currentGroupBy);
    params.set("window", overrides.window ?? currentWindow);
    return `/console/analytics?${params.toString()}`;
  }

  function handleGroupByChange(e: ChangeEvent<HTMLSelectElement>) {
    router.push(buildUrl({ group_by: e.target.value }));
  }

  function handleWindowChange(window: string) {
    router.push(buildUrl({ window }));
  }

  return (
    <div className="mb-6 flex flex-col gap-4 sm:flex-row sm:items-end sm:justify-between">
      <Field label="Group by" htmlFor="group-by" className="sm:w-48">
        <select
          id="group-by"
          value={currentGroupBy}
          onChange={handleGroupByChange}
          className={SELECT_CLASSES}
        >
          <option value="model">Model</option>
          <option value="api_key">API key</option>
          <option value="endpoint">Endpoint</option>
        </select>
      </Field>
      <Field label="Time window" htmlFor="time-window" className="sm:w-auto">
        <TimeWindowPicker
          currentWindow={currentWindow}
          onWindowChange={handleWindowChange}
        />
      </Field>
    </div>
  );
}
