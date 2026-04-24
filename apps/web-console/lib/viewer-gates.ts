export interface ViewerGates {
  can_invite_members: boolean;
  can_manage_api_keys: boolean;
}

export interface ViewerForGates {
  gates: ViewerGates;
}

export function canInviteMembers(viewer: ViewerForGates): boolean {
  return viewer.gates.can_invite_members;
}

export function canManageApiKeys(viewer: ViewerForGates): boolean {
  return viewer.gates.can_manage_api_keys;
}

export const allowedUnverifiedRoutes: string[] = [
  "/console",
  "/console/setup",
  "/console/settings/profile",
];
