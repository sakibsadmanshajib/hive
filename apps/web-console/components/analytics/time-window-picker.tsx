"use client";

import { useState } from "react";

import { Button } from "@/components/ui/button";
import { Field, Input } from "@/components/ui/input";
import { cn } from "@/lib/cn";

interface TimeWindowPickerProps {
  currentWindow: string;
  onWindowChange: (window: string) => void;
}

interface PresetWindow {
  value: string;
  label: string;
}

const PRESET_WINDOWS: ReadonlyArray<PresetWindow> = [
  { value: "24h", label: "24h" },
  { value: "7d", label: "7d" },
  { value: "30d", label: "30d" },
  { value: "90d", label: "90d" },
];

export function TimeWindowPicker({
  currentWindow,
  onWindowChange,
}: TimeWindowPickerProps) {
  const [showCustom, setShowCustom] = useState(false);
  const [fromDate, setFromDate] = useState("");
  const [toDate, setToDate] = useState("");

  function handlePresetClick(value: string) {
    setShowCustom(false);
    onWindowChange(value);
  }

  function handleCustomApply() {
    if (fromDate && toDate) {
      onWindowChange(`custom:${fromDate}:${toDate}`);
    }
  }

  const isCustomActive = currentWindow.startsWith("custom:");

  return (
    <div className="flex flex-col gap-3">
      <div
        className="inline-flex items-center rounded-md border border-[var(--color-border)] bg-[var(--color-surface)] p-0.5"
        role="group"
        aria-label="Time window"
      >
        {PRESET_WINDOWS.map((preset) => {
          const isActive = currentWindow === preset.value;
          return (
            <button
              key={preset.value}
              type="button"
              onClick={() => handlePresetClick(preset.value)}
              className={cn(
                "h-7 rounded px-3 text-xs transition-colors",
                isActive
                  ? "bg-[var(--color-ink)] text-[var(--color-canvas)]"
                  : "text-[var(--color-ink-2)] hover:bg-[var(--color-surface-2)]",
              )}
            >
              {preset.label}
            </button>
          );
        })}
        <button
          type="button"
          onClick={() => setShowCustom(true)}
          className={cn(
            "h-7 rounded px-3 text-xs transition-colors",
            isCustomActive
              ? "bg-[var(--color-ink)] text-[var(--color-canvas)]"
              : "text-[var(--color-ink-2)] hover:bg-[var(--color-surface-2)]",
          )}
        >
          Custom
        </button>
      </div>
      {showCustom ? (
        <div className="flex flex-wrap items-end gap-2">
          <Field label="From" htmlFor="window-from">
            <Input
              id="window-from"
              type="date"
              value={fromDate}
              onChange={(e) => setFromDate(e.target.value)}
              className="w-40"
            />
          </Field>
          <Field label="To" htmlFor="window-to">
            <Input
              id="window-to"
              type="date"
              value={toDate}
              onChange={(e) => setToDate(e.target.value)}
              className="w-40"
            />
          </Field>
          <Button
            type="button"
            variant="primary"
            size="sm"
            onClick={handleCustomApply}
            disabled={!fromDate || !toDate}
          >
            Apply
          </Button>
        </div>
      ) : null}
    </div>
  );
}
