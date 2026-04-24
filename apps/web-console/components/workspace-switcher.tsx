"use client";

import type { ViewerMembership, ViewerAccount } from "@/lib/control-plane/client";

interface WorkspaceSwitcherProps {
  memberships: ViewerMembership[];
  currentAccount: ViewerAccount;
}

export function WorkspaceSwitcher({
  memberships,
  currentAccount,
}: WorkspaceSwitcherProps) {
  return (
    <div
      style={{
        padding: "0.75rem 1rem",
        borderBottom: "1px solid #e5e7eb",
        fontSize: "0.875rem",
      }}
    >
      <div style={{ fontWeight: 500, marginBottom: "0.5rem", color: "#6b7280" }}>
        Workspace
      </div>
      <form method="POST" action="/console/account-switch">
        <select
          name="account_id"
          defaultValue={currentAccount.id}
          style={{
            width: "100%",
            padding: "0.375rem 0.5rem",
            border: "1px solid #d1d5db",
            borderRadius: "0.375rem",
            fontSize: "0.875rem",
            cursor: "pointer",
          }}
          onChange={(e) => {
            e.currentTarget.form?.requestSubmit();
          }}
        >
          {memberships.map((m) => (
            <option key={m.account_id} value={m.account_id}>
              {m.account_display_name}
              {m.account_id === currentAccount.id ? " (current)" : ""}
            </option>
          ))}
        </select>
      </form>
    </div>
  );
}
