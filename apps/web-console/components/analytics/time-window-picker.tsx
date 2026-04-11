"use client";

import { useState } from "react";

interface TimeWindowPickerProps {
  currentWindow: string;
  onWindowChange: (window: string) => void;
}

const PRESET_WINDOWS = [
  { value: "24h", label: "Last 24 hours" },
  { value: "7d", label: "Last 7 days" },
  { value: "30d", label: "Last 30 days" },
  { value: "90d", label: "Last 90 days" },
];

export function TimeWindowPicker({ currentWindow, onWindowChange }: TimeWindowPickerProps) {
  const [showCustom, setShowCustom] = useState(false);
  const [fromDate, setFromDate] = useState("");
  const [toDate, setToDate] = useState("");

  function handlePresetClick(value: string) {
    setShowCustom(false);
    onWindowChange(value);
  }

  function handleCustomClick() {
    setShowCustom(true);
  }

  function handleCustomApply() {
    if (fromDate && toDate) {
      onWindowChange(`custom:${fromDate}:${toDate}`);
    }
  }

  const isCustomActive = currentWindow.startsWith("custom:");

  return (
    <div>
      <div style={{ display: "flex", gap: "0.5rem", flexWrap: "wrap" }}>
        {PRESET_WINDOWS.map((preset) => {
          const isActive = currentWindow === preset.value;
          return (
            <button
              key={preset.value}
              onClick={() => handlePresetClick(preset.value)}
              style={{
                padding: "0.375rem 0.75rem",
                fontSize: "0.875rem",
                borderRadius: "0.375rem",
                border: "1px solid",
                cursor: "pointer",
                backgroundColor: isActive ? "#1d4ed8" : "transparent",
                color: isActive ? "#ffffff" : "#374151",
                borderColor: isActive ? "#1d4ed8" : "#d1d5db",
                fontWeight: isActive ? 600 : 400,
              }}
            >
              {preset.label}
            </button>
          );
        })}
        <button
          onClick={handleCustomClick}
          style={{
            padding: "0.375rem 0.75rem",
            fontSize: "0.875rem",
            borderRadius: "0.375rem",
            border: "1px solid",
            cursor: "pointer",
            backgroundColor: isCustomActive ? "#1d4ed8" : "transparent",
            color: isCustomActive ? "#ffffff" : "#374151",
            borderColor: isCustomActive ? "#1d4ed8" : "#d1d5db",
            fontWeight: isCustomActive ? 600 : 400,
          }}
        >
          Custom range
        </button>
      </div>
      {showCustom && (
        <div
          style={{
            marginTop: "0.75rem",
            display: "flex",
            gap: "0.75rem",
            alignItems: "flex-end",
            flexWrap: "wrap",
          }}
        >
          <div style={{ display: "flex", flexDirection: "column", gap: "0.25rem" }}>
            <label style={{ fontSize: "0.75rem", color: "#6b7280", fontWeight: 500 }}>From</label>
            <input
              type="date"
              value={fromDate}
              onChange={(e) => setFromDate(e.target.value)}
              style={{
                padding: "0.375rem 0.5rem",
                fontSize: "0.875rem",
                border: "1px solid #d1d5db",
                borderRadius: "0.375rem",
              }}
            />
          </div>
          <div style={{ display: "flex", flexDirection: "column", gap: "0.25rem" }}>
            <label style={{ fontSize: "0.75rem", color: "#6b7280", fontWeight: 500 }}>To</label>
            <input
              type="date"
              value={toDate}
              onChange={(e) => setToDate(e.target.value)}
              style={{
                padding: "0.375rem 0.5rem",
                fontSize: "0.875rem",
                border: "1px solid #d1d5db",
                borderRadius: "0.375rem",
              }}
            />
          </div>
          <button
            onClick={handleCustomApply}
            disabled={!fromDate || !toDate}
            style={{
              padding: "0.375rem 0.75rem",
              fontSize: "0.875rem",
              borderRadius: "0.375rem",
              border: "1px solid #1d4ed8",
              cursor: fromDate && toDate ? "pointer" : "not-allowed",
              backgroundColor: fromDate && toDate ? "#1d4ed8" : "#e5e7eb",
              color: fromDate && toDate ? "#ffffff" : "#9ca3af",
              fontWeight: 600,
            }}
          >
            Apply
          </button>
        </div>
      )}
    </div>
  );
}
