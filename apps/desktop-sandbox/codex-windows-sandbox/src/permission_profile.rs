//! Minimal, Hive-authored stand-in for upstream `codex_protocol::models::PermissionProfile`.
//!
//! NOT vendored from upstream. `elevated/ipc_framed.rs`'s `SpawnRequest` struct carries a
//! `permission_profile` field of this type as part of the wire schema (see that file's own
//! module doc). Upstream's real `PermissionProfile` is a much larger tagged enum with legacy
//! serde-migration variants (`Managed`, `Disabled`, `External`, plus a hand-rolled
//! `Deserialize` impl for backward compatibility with older rollout files); consuming its
//! variants meaningfully requires `ResolvedWindowsSandboxPermissions::try_from_permission_profile*`
//! (upstream's `resolved_permissions.rs`), which Wave 1 deliberately ported to Hive-native
//! types (`hive-desktop-sandbox/src/windows_resolve.rs`) rather than vendoring, to avoid a
//! `codex_protocol` dependency anywhere in this tree (Step 3 Wave 2 dependency-tangle rule).
//!
//! This stand-in exists ONLY so `elevated/ipc_framed.rs`'s struct definition and its
//! `PermissionProfile::read_only()` test construction compile; nothing in this wave resolves
//! or interprets a `PermissionProfile` value (`spawn_prep.rs`, `wrapper.rs`, `elevated_impl.rs`,
//! `identity.rs`'s resolver-coupled functions, and the full `setup.rs` are excluded this wave;
//! see `VENDORING.md`). Do not extend this type's variants to match upstream without also
//! porting a real resolver for it, or it will silently misrepresent the vendored wire format.

use serde::Deserialize;
use serde::Serialize;

/// Placeholder shape for the real upstream `PermissionProfile` tagged enum. Only the
/// `read_only()` constructor upstream's own `elevated/ipc_framed.rs` test exercises is
/// reproduced.
#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
pub struct PermissionProfile {
    pub read_only: bool,
}

impl PermissionProfile {
    pub fn read_only() -> Self {
        Self { read_only: true }
    }
}
