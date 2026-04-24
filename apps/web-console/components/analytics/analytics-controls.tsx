"use client";

import { useRouter } from "next/navigation";
import { TimeWindowPicker } from "./time-window-picker";

interface AnalyticsControlsProps {
  currentGroupBy: string;
  currentWindow: string;
  activeTab: string;
}

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

  function handleGroupByChange(e: React.ChangeEvent<HTMLSelectElement>) {
    router.push(buildUrl({ group_by: e.target.value }));
  }

  function handleWindowChange(window: string) {
    router.push(buildUrl({ window }));
  }

  return (
    <div
      style={{
        display: "flex",
        gap: "1rem",
        alignItems: "flex-start",
        flexWrap: "wrap",
        marginBottom: "1.5rem",
      }}
    >
      <div style={{ display: "flex", flexDirection: "column", gap: "0.25rem" }}>
        <label
          htmlFor="group-by"
          style={{ fontSize: "0.75rem", color: "#6b7280", fontWeight: 500 }}
        >
          Group by
        </label>
        <select
          id="group-by"
          value={currentGroupBy}
          onChange={handleGroupByChange}
          style={{
            padding: "0.375rem 0.5rem",
            fontSize: "0.875rem",
            border: "1px solid #d1d5db",
            borderRadius: "0.375rem",
            backgroundColor: "#ffffff",
            cursor: "pointer",
          }}
        >
          <option value="model">Model</option>
          <option value="api_key">API Key</option>
          <option value="endpoint">Endpoint</option>
        </select>
      </div>
      <div style={{ display: "flex", flexDirection: "column", gap: "0.25rem" }}>
        <span style={{ fontSize: "0.75rem", color: "#6b7280", fontWeight: 500 }}>
          Time window
        </span>
        <TimeWindowPicker
          currentWindow={currentWindow}
          onWindowChange={handleWindowChange}
        />
      </div>
    </div>
  );
}
