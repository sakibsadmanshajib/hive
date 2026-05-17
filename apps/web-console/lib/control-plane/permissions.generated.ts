// AUTO-GENERATED — do not edit. Run `make gen-permissions` to regenerate.
// Source: apps/control-plane/internal/authz/permissions.go

export const PERMISSIONS = [
  "analytics.view",
  "api_keys.read",
  "api_keys.write",
  "billing.view",
  "billing.write",
  "grants.create",
  "ledger.view",
  "members.invite",
  "members.manage",
  "platform.admin",
  "workspace.settings",
] as const;

export type Permission = typeof PERMISSIONS[number];
