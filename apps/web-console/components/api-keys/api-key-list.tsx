import type { ApiKey } from "@/lib/control-plane/client";
import { RevokeConfirmPanel } from "./revoke-confirm-panel";

interface ApiKeyListProps {
  keys: ApiKey[];
  canManage: boolean;
}

function statusStyle(status: string): { label: string; color: string } {
  switch (status) {
    case "active":
      return { label: "Active", color: "#10b981" };
    case "revoked":
      return { label: "Revoked", color: "#dc2626" };
    case "expired":
      return { label: "Expired", color: "#6b7280" };
    default:
      return { label: status, color: "#6b7280" };
  }
}

function formatDate(isoString: string | null): string {
  if (!isoString) {
    return "—";
  }
  try {
    return new Date(isoString).toLocaleDateString("en-US", {
      year: "numeric",
      month: "short",
      day: "numeric",
    });
  } catch {
    return isoString;
  }
}

export function ApiKeyList({ keys, canManage }: ApiKeyListProps) {
  if (keys.length === 0) {
    return (
      <div
        style={{
          border: "1px solid #d1d5db",
          borderRadius: "0.75rem",
          padding: "2rem",
          textAlign: "center",
          display: "grid",
          gap: "0.5rem",
        }}
      >
        <p style={{ margin: 0, fontWeight: 700, color: "#1f2937" }}>No API keys</p>
        <p style={{ margin: 0, color: "#6b7280" }}>
          Create your first API key to start making requests to the Hive API.
        </p>
      </div>
    );
  }

  return (
    <table style={{ width: "100%", borderCollapse: "collapse" }}>
      <thead>
        <tr>
          <th
            style={{
              padding: "0.5rem 0.75rem",
              textAlign: "left",
              fontWeight: 700,
              fontSize: "0.875rem",
              borderBottom: "1px solid #e5e7eb",
              color: "#4b5563",
            }}
          >
            Nickname
          </th>
          <th
            style={{
              padding: "0.5rem 0.75rem",
              textAlign: "left",
              fontWeight: 700,
              fontSize: "0.875rem",
              borderBottom: "1px solid #e5e7eb",
              color: "#4b5563",
            }}
          >
            Status
          </th>
          <th
            style={{
              padding: "0.5rem 0.75rem",
              textAlign: "left",
              fontWeight: 700,
              fontSize: "0.875rem",
              borderBottom: "1px solid #e5e7eb",
              color: "#4b5563",
            }}
          >
            Key
          </th>
          <th
            style={{
              padding: "0.5rem 0.75rem",
              textAlign: "left",
              fontWeight: 700,
              fontSize: "0.875rem",
              borderBottom: "1px solid #e5e7eb",
              color: "#4b5563",
            }}
          >
            Expires
          </th>
          <th
            style={{
              padding: "0.5rem 0.75rem",
              textAlign: "left",
              fontWeight: 700,
              fontSize: "0.875rem",
              borderBottom: "1px solid #e5e7eb",
              color: "#4b5563",
            }}
          >
            Last Used
          </th>
          {canManage && (
            <th
              style={{
                padding: "0.5rem 0.75rem",
                textAlign: "left",
                fontWeight: 700,
                fontSize: "0.875rem",
                borderBottom: "1px solid #e5e7eb",
                color: "#4b5563",
              }}
            >
              Actions
            </th>
          )}
        </tr>
      </thead>
      <tbody>
        {keys.map((key) => {
          const { label: statusLabel, color: statusColor } = statusStyle(key.status);
          const isActive = key.status === "active";

          return (
            <tr key={key.id}>
              <td
                style={{
                  padding: "0.5rem 0.75rem",
                  borderBottom: "1px solid #f3f4f6",
                  fontSize: "1rem",
                  fontWeight: 700,
                }}
              >
                {key.nickname}
              </td>
              <td
                style={{
                  padding: "0.5rem 0.75rem",
                  borderBottom: "1px solid #f3f4f6",
                  fontSize: "1rem",
                  color: statusColor,
                  fontWeight: 700,
                }}
              >
                {statusLabel}
              </td>
              <td
                style={{
                  padding: "0.5rem 0.75rem",
                  borderBottom: "1px solid #f3f4f6",
                  fontSize: "1rem",
                  fontFamily: "monospace",
                  color: "#6b7280",
                }}
              >
                •••• {key.redacted_suffix}
              </td>
              <td
                style={{
                  padding: "0.5rem 0.75rem",
                  borderBottom: "1px solid #f3f4f6",
                  fontSize: "1rem",
                  color: "#6b7280",
                }}
              >
                {formatDate(key.expires_at)}
              </td>
              <td
                style={{
                  padding: "0.5rem 0.75rem",
                  borderBottom: "1px solid #f3f4f6",
                  fontSize: "1rem",
                  color: "#6b7280",
                }}
              >
                {formatDate(key.last_used_at)}
              </td>
              {canManage && (
                <td
                  style={{
                    padding: "0.5rem 0.75rem",
                    borderBottom: "1px solid #f3f4f6",
                    fontSize: "1rem",
                  }}
                >
                  {isActive && (
                    <div style={{ display: "flex", gap: "0.5rem", flexWrap: "wrap" }}>
                      <a
                        href={`/console/api-keys/${key.id}/rotate`}
                        style={{
                          color: "#1d4ed8",
                          fontSize: "0.875rem",
                          textDecoration: "none",
                        }}
                      >
                        Rotate key
                      </a>
                      <RevokeConfirmPanel
                        keyId={key.id}
                        keyNickname={key.nickname}
                      />
                    </div>
                  )}
                </td>
              )}
            </tr>
          );
        })}
      </tbody>
    </table>
  );
}
