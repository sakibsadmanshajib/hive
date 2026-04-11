import { getApiKeys, getViewer } from "@/lib/control-plane/client";
import { ApiKeyList } from "@/components/api-keys/api-key-list";
import { ApiKeyCreateForm } from "@/components/api-keys/api-key-create-form";

export default async function ApiKeysPage() {
  const [keys, viewer] = await Promise.all([getApiKeys(), getViewer()]);
  const canManage = viewer.gates.can_manage_api_keys;

  return (
    <div style={{ display: "grid", gap: "1.5rem", maxWidth: "72rem" }}>
      <div style={{ display: "flex", alignItems: "center", justifyContent: "space-between" }}>
        <h1 style={{ margin: 0, fontSize: "1.5rem", fontWeight: 700 }}>API Keys</h1>
      </div>

      {canManage && <ApiKeyCreateForm />}

      <ApiKeyList keys={keys} canManage={canManage} />
    </div>
  );
}
