import type { CatalogModel } from "@/lib/control-plane/client";

interface ModelCatalogTableProps {
  models: CatalogModel[];
}

function lifecycleStatus(lifecycle: string): { label: string; color: string } {
  if (lifecycle === "active") {
    return { label: "Available", color: "#10b981" };
  }
  return { label: "Unavailable", color: "#6b7280" };
}

export function ModelCatalogTable({ models }: ModelCatalogTableProps) {
  if (models.length === 0) {
    return (
      <p style={{ margin: 0, color: "#6b7280" }}>
        No models available. Check back soon.
      </p>
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
            Model
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
            Capabilities
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
            Input (per 1M tokens)
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
            Output (per 1M tokens)
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
        </tr>
      </thead>
      <tbody>
        {models.map((model) => {
          const { label: statusLabel, color: statusColor } = lifecycleStatus(model.lifecycle);
          const capabilities = model.capability_badges.join(", ");

          return (
            <tr key={model.id}>
              <td
                style={{
                  padding: "0.5rem 0.75rem",
                  borderBottom: "1px solid #f3f4f6",
                  fontSize: "1rem",
                  fontWeight: 700,
                }}
              >
                <div>{model.display_name}</div>
                {model.summary && (
                  <div
                    style={{
                      fontSize: "0.875rem",
                      color: "#6b7280",
                      fontWeight: 400,
                      marginTop: "0.125rem",
                    }}
                  >
                    {model.summary}
                  </div>
                )}
              </td>
              <td
                style={{
                  padding: "0.5rem 0.75rem",
                  borderBottom: "1px solid #f3f4f6",
                  fontSize: "1rem",
                  color: "#4b5563",
                }}
              >
                {capabilities || "—"}
              </td>
              <td
                style={{
                  padding: "0.5rem 0.75rem",
                  borderBottom: "1px solid #f3f4f6",
                  fontSize: "1rem",
                }}
              >
                {model.pricing.input_price_credits.toLocaleString()}
              </td>
              <td
                style={{
                  padding: "0.5rem 0.75rem",
                  borderBottom: "1px solid #f3f4f6",
                  fontSize: "1rem",
                }}
              >
                {model.pricing.output_price_credits.toLocaleString()}
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
            </tr>
          );
        })}
      </tbody>
    </table>
  );
}
