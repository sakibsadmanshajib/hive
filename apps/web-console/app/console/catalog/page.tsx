import { getCatalogModels } from "@/lib/control-plane/client";
import { ModelCatalogTable } from "@/components/catalog/model-catalog-table";

export default async function CatalogPage() {
  const models = await getCatalogModels();

  return (
    <div style={{ display: "grid", gap: "1.5rem", maxWidth: "72rem" }}>
      <h1 style={{ margin: 0, fontSize: "1.5rem", fontWeight: 700 }}>Model Catalog</h1>
      <ModelCatalogTable models={models} />
    </div>
  );
}
