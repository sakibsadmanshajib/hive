import { getApiKeys, getViewer } from "@/lib/control-plane/client";
import { ApiKeyList } from "@/components/api-keys/api-key-list";
import { ApiKeyCreateForm } from "@/components/api-keys/api-key-create-form";
import { redirect } from "next/navigation";

export default async function ApiKeysPage() {
  const viewer = await getViewer();
  const canManage = viewer.gates.can_manage_api_keys;
  if (!canManage) {
    redirect("/console/settings/profile");
  }

  const keys = await getApiKeys();

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
