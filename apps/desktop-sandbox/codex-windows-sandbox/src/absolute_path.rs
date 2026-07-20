//! Hive-authored, traversal-guarded stand-in for upstream
//! `codex_utils_absolute_path::AbsolutePathBuf`.
//!
//! NOT vendored from upstream. The real `codex-utils-absolute-path` crate is
//! ~900 lines with `schemars`/`ts-rs` (TypeScript binding generation)
//! dependencies, a thread-local base-path guard for deserialization, and
//! home-directory expansion, none of which is load-bearing here. This
//! stand-in exists so `elevated/ipc_framed.rs`'s
//! `SpawnRequest.workspace_roots: Vec<AbsolutePathBuf>` field resolves and
//! (de)serializes.
//!
//! Integration A1 hardening (W2 review finding 4): workspace roots ride into
//! the elevated runner over IPC and are later handed to the ACL grant path, so
//! this type is now a validating newtype rather than an opaque wrapper. Every
//! value, however constructed (including via `serde` from an inbound frame), is
//! checked to be an absolute Windows path (drive-absolute `C:\...`/`C:/...` or
//! UNC/device `\\...`) that contains no `..` traversal segment. A crafted frame
//! carrying `C:\ws\..\..\Windows\System32` is therefore rejected at the
//! deserialization boundary, before it can widen an ACL grant. The inner
//! `PathBuf` is private; callers read it through [`AbsolutePathBuf::as_path`].

use serde::Deserialize;
use serde::Serialize;
use std::path::Path;
use std::path::PathBuf;

/// Rejected [`AbsolutePathBuf`] input.
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum AbsolutePathError {
    /// Path is not an absolute Windows path (drive-absolute or UNC/device).
    NotAbsolute(PathBuf),
    /// Path contains a `..` traversal segment.
    Traversal(PathBuf),
}

impl std::fmt::Display for AbsolutePathError {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        match self {
            AbsolutePathError::NotAbsolute(path) => {
                write!(f, "not an absolute Windows path: {}", path.display())
            }
            AbsolutePathError::Traversal(path) => {
                write!(
                    f,
                    "path contains a `..` traversal segment: {}",
                    path.display()
                )
            }
        }
    }
}

impl std::error::Error for AbsolutePathError {}

/// An absolute, traversal-free Windows path.
#[derive(Debug, Clone, PartialEq, Eq, Serialize)]
pub struct AbsolutePathBuf(PathBuf);

impl AbsolutePathBuf {
    /// Validates `path` and wraps it, or rejects it. Enforced invariants:
    /// the path is an absolute Windows path and contains no `..` segment.
    pub fn new(path: impl Into<PathBuf>) -> Result<Self, AbsolutePathError> {
        let path = path.into();
        let raw = path.to_string_lossy();
        if !is_windows_absolute(&raw) {
            return Err(AbsolutePathError::NotAbsolute(path));
        }
        // String-based segment scan: `Path::components` uses HOST separator
        // rules, so on this crate's Linux CI a Windows path's `\` segments are
        // not split and a `..` would slip through. Split on both separators.
        if raw.split(['/', '\\']).any(|seg| seg == "..") {
            return Err(AbsolutePathError::Traversal(path));
        }
        Ok(Self(path))
    }

    /// Borrow the validated path.
    pub fn as_path(&self) -> &Path {
        &self.0
    }
}

impl<'de> Deserialize<'de> for AbsolutePathBuf {
    fn deserialize<D>(deserializer: D) -> Result<Self, D::Error>
    where
        D: serde::Deserializer<'de>,
    {
        // Validate at the trust boundary: an inbound IPC frame is untrusted.
        let path = PathBuf::deserialize(deserializer)?;
        AbsolutePathBuf::new(path).map_err(serde::de::Error::custom)
    }
}

/// True for a drive-absolute (`C:\...`, `C:/...`) or UNC/device
/// (`\\server\share`, `\\?\C:\...`) Windows path. Deliberately not
/// `Path::is_absolute()`, which uses HOST conventions and would reject every
/// Windows path on this crate's Linux CI. Same technique as
/// `hive_desktop_sandbox::windows_resolve::is_windows_absolute`.
fn is_windows_absolute(raw: &str) -> bool {
    let bytes = raw.as_bytes();
    let is_sep = |b: u8| b == b'\\' || b == b'/';
    if bytes.len() >= 2 && is_sep(bytes[0]) && is_sep(bytes[1]) {
        return true;
    }
    bytes.len() >= 3 && bytes[0].is_ascii_alphabetic() && bytes[1] == b':' && is_sep(bytes[2])
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn accepts_drive_absolute_path() {
        let abs = AbsolutePathBuf::new(PathBuf::from(r"C:\workspace\project")).expect("valid");
        assert_eq!(abs.as_path(), Path::new(r"C:\workspace\project"));
    }

    #[test]
    fn accepts_unc_path() {
        assert!(AbsolutePathBuf::new(PathBuf::from(r"\\server\share\work")).is_ok());
    }

    #[test]
    fn rejects_relative_path() {
        assert_eq!(
            AbsolutePathBuf::new(PathBuf::from(r"workspace\project")),
            Err(AbsolutePathError::NotAbsolute(PathBuf::from(
                r"workspace\project"
            )))
        );
    }

    #[test]
    fn rejects_backslash_traversal_segment() {
        assert_eq!(
            AbsolutePathBuf::new(PathBuf::from(r"C:\ws\..\..\Windows\System32")),
            Err(AbsolutePathError::Traversal(PathBuf::from(
                r"C:\ws\..\..\Windows\System32"
            )))
        );
    }

    #[test]
    fn rejects_forward_slash_traversal_segment() {
        assert!(matches!(
            AbsolutePathBuf::new(PathBuf::from("C:/ws/../secret")),
            Err(AbsolutePathError::Traversal(_))
        ));
    }

    #[test]
    fn deserialize_rejects_traversal_from_untrusted_frame() {
        let err = serde_json::from_str::<AbsolutePathBuf>(r#""C:\\ws\\..\\etc""#)
            .expect_err("a `..` path from an inbound frame must be rejected");
        assert!(err.to_string().contains("traversal"), "unexpected: {err}");
    }

    #[test]
    fn deserialize_accepts_valid_absolute_path() {
        let abs = serde_json::from_str::<AbsolutePathBuf>(r#""C:\\workspace""#).expect("valid");
        assert_eq!(abs.as_path(), Path::new(r"C:\workspace"));
    }

    #[test]
    fn round_trips_through_serde() {
        let abs = AbsolutePathBuf::new(PathBuf::from(r"C:\workspace")).expect("valid");
        let json = serde_json::to_string(&abs).expect("serialize");
        let back: AbsolutePathBuf = serde_json::from_str(&json).expect("deserialize");
        assert_eq!(abs, back);
    }
}
