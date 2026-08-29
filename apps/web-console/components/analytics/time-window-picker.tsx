"use client";

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

// Presets only, and deliberately the four the analytics summary endpoint's
// own window enum recognizes (parseAnalyticsFilter,
// apps/control-plane/internal/usage/http.go). A Custom control used to sit
// here and emit "custom:from:to", which no fetch on this page understood: the
// page fell back to 7d and rendered seven days of data under a heading naming
// the range the user picked. A control that discards the input it collects is
// worse than no control, so it is gone until the range is threaded through
// the fetches for real. Tracked in issue #1338.
export function TimeWindowPicker({
  currentWindow,
  onWindowChange,
}: TimeWindowPickerProps) {
  return (
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
            onClick={() => onWindowChange(preset.value)}
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
    </div>
  );
}
