//! Minimal, Hive-authored stand-in for upstream `codex_utils_absolute_path::AbsolutePathBuf`.
//!
//! NOT vendored from upstream. The real `codex-utils-absolute-path` crate is ~900 lines with
//! `schemars`/`ts-rs` (TypeScript binding generation) dependencies, a thread-local base-path
//! guard for deserialization, and home-directory expansion, none of which is load-bearing for
//! this wave: `elevated/ipc_framed.rs`'s `SpawnRequest.workspace_roots: Vec<AbsolutePathBuf>`
//! field is inert (nothing constructs a real value this wave; the one upstream test that did,
//! `spawn_request_serializes_permission_profile`, is excluded, see that file's module doc).
//! This stand-in exists only so the struct field type resolves and (de)serializes.
//!
//! Pulling in the real crate's `schemars`/`ts-rs` dependency chain for a single opaque,
//! unexercised field would be scope creep for an explicitly inert vendor wave; revisit if a
//! later wave actually constructs or inspects workspace roots through this type.

use serde::Deserialize;
use serde::Serialize;
use std::path::PathBuf;

#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
pub struct AbsolutePathBuf(PathBuf);
