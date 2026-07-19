//! Port of upstream `resolved_permissions.rs`
//! (`codex-rs/windows-sandbox-rs/src/resolved_permissions.rs`, commit
//! `a47c661…`, see `../../VENDORING.md`) to Hive-native types.
//!
//! This is the ONE adapter seam blueprint Step 3 identifies: upstream's
//! `ResolvedWindowsSandboxPermissions` is the only windows-sandbox module that
//! imports `codex_protocol` (`PermissionProfile`, `FileSystemSandboxPolicy`,
//! `NetworkSandboxPolicy`, ...). Every vendored mechanism module in
//! `codex-windows-sandbox` (ACLs, tokens, capability SIDs, ...) takes
//! primitive arguments (paths, masks, SID handles) and never speaks
//! `codex_protocol`, so replacing this one seam is what lets the rest of the
//! upstream crate be vendored byte-for-byte instead of ported. `codex_protocol`
//! is deliberately NOT a dependency of this crate or of `codex-windows-sandbox`
//! (see `Cargo.toml`): every type below is Hive-native.
//!
//! Unlike upstream, [`SandboxPolicy`] is already a fully resolved, concrete
//! policy (explicit `PathBuf` roots, no globs, no symbolic `:workspace_roots`
//! placeholders, no Managed/Disabled/Unrestricted profile distinction), so
//! this port is much thinner than upstream's `try_from_permission_profile*`:
//! there is no glob expansion, no project-roots symbolic-path materialization,
//! and no "full disk write" rejection to replicate, because Hive's policy
//! model cannot express any of those in the first place. Three upstream
//! accessors have no Hive equivalent and are deliberately NOT ported:
//! `is_enforceable_by_windows_sandbox`, `has_full_disk_read_access`, and
//! `include_platform_defaults` all distinguish between profile KINDS
//! (Managed/Disabled/Unrestricted, Restricted/Unrestricted filesystem) that
//! [`SandboxPolicy`] does not have; every [`SandboxPolicy`] is, in upstream's
//! terms, already the single concrete "restricted, managed" case.
//!
//! This module has no Win32 dependency and is pure, host-independent
//! computation: it compiles and its tests run on every platform, including
//! this crate's Linux CI job, exactly like `windows_plan.rs`. It performs no
//! Win32 calls and is not wired into `windows::launch` this wave (Step 3 Wave
//! 1); see `../../VENDORING.md`.

use crate::SandboxPolicy;
use crate::policy::NetworkPolicy;
use std::path::{Path, PathBuf};

/// A single writable root and any read-only carve-outs nested inside it.
///
/// Mirrors upstream's `WindowsWritableRoot` shape (kept for continuity with
/// the vendored mechanism modules' naming and for future extension), but
/// `read_only_subpaths` is always empty from [`ResolvedWindowsSandboxPermissions::from_policy`]
/// today: `SandboxPolicy` has no concept yet of a read-only carve-out nested
/// inside an otherwise-writable root (upstream derives this from per-entry
/// glob/path deny overrides within a Managed profile, which `SandboxPolicy`
/// does not model). A future `SandboxPolicy` extension can populate this
/// field without changing this type's shape.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct WindowsWritableRoot {
    pub root: PathBuf,
    pub read_only_subpaths: Vec<PathBuf>,
}

/// Restricted-token family to enforce a resolved policy under.
///
/// Upstream chooses between `ReadOnlyCapability` and `WritableRootsCapability`
/// per launch (`token_mode_for_permission_profile`), because its base
/// (non-elevated) token variant is the only one it has. Hive's Step 3 scope
/// is the elevated sandbox-user variant exclusively (see the blueprint's Q1/Q3
/// resolution): the base non-elevated restricted token from issue #395 is not
/// reused here, so there is exactly one variant to select today. This enum
/// exists (rather than hardcoding the value inline) so a future elevated-vs-
/// unprivileged distinction has somewhere to go without changing every call
/// site.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum WindowsSandboxTokenMode {
    /// The Step 3 elevated helper's dedicated low-privilege sandbox OS-user
    /// token (see the blueprint's section 3, "Elevated-runner architecture").
    /// Not implemented yet (Wave 3); this variant exists so callers can match
    /// on it ahead of that wave.
    ElevatedSandboxUser,
}

/// Rejected [`ResolvedWindowsSandboxPermissions::from_policy`] input. Mirrors
/// [`crate::policy::PolicyError`]'s philosophy: fail loudly rather than
/// resolve a policy with a weaker guarantee than requested (D-005).
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum ResolveError {
    /// `cwd` was not an absolute path. Every root this type hands back is
    /// meant to be a concrete ACL target; a relative `cwd` would make
    /// [`ResolvedWindowsSandboxPermissions::readonly_roots`] silently resolve
    /// against whatever the current process's working directory happens to be
    /// at ACL-apply time, instead of the caller's intended directory. Upstream
    /// enforces the equivalent invariant at the type level (`AbsolutePathBuf`
    /// for `cwd`/workspace roots); `SandboxPolicy` has no such newtype, so this
    /// is a runtime check instead.
    CwdNotAbsolute { cwd: PathBuf },
}

impl std::fmt::Display for ResolveError {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        match self {
            ResolveError::CwdNotAbsolute { cwd } => {
                write!(f, "cwd must be an absolute path, got: {}", cwd.display())
            }
        }
    }
}

impl std::error::Error for ResolveError {}

/// Hive-native replacement for upstream's `ResolvedWindowsSandboxPermissions`.
/// The internal resolved view every Windows mechanism module should consume
/// (once wired: this wave stops at the adapter, see the module doc).
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct ResolvedWindowsSandboxPermissions {
    writable_roots: Vec<WindowsWritableRoot>,
    readonly_roots: Vec<PathBuf>,
    deny_read_paths: Vec<PathBuf>,
    network: NetworkPolicy,
    token_mode: WindowsSandboxTokenMode,
}

impl ResolvedWindowsSandboxPermissions {
    /// Resolves a [`SandboxPolicy`] into the Windows-specific view. See the
    /// field-by-field mapping table in the blueprint (section 2).
    ///
    /// - `policy.writable_roots()` -> [`Self::writable_roots`], one
    ///   [`WindowsWritableRoot`] per root (no nested read-only carve-outs yet).
    /// - `policy.readonly_roots()` PLUS `policy.hook_config_dir()` ->
    ///   [`Self::readonly_roots`]. The hook/config directory is always
    ///   readable; the deny-write ACE on its PARENT (the #307 CBSE
    ///   invariant) is a separate, out-of-band control `windows.rs` applies
    ///   directly (see [`crate::windows_plan::WindowsConfinementPlan`]), not
    ///   part of this resolved-permissions type.
    /// - Secret/deny-read paths -> [`Self::deny_read_paths`]. Always empty
    ///   today: `SandboxPolicy` has no secret-path input source yet (the
    ///   blueprint notes this is a future hook/config-derived input), so an
    ///   empty carve-out set is the honest default rather than inventing one.
    ///   Upstream's `deny_read_resolver.rs` (glob-scan-based carve-out
    ///   selection) is deliberately NOT ported this wave for the same reason
    ///   this field is always empty: there is nothing yet to resolve.
    /// - `policy.network()` -> [`Self::network`], carried through unchanged
    ///   (both variants; enforcement stays deferred to Step 4/the elevated
    ///   wave, this is just the adapter).
    /// - Token mode is constant: [`WindowsSandboxTokenMode::ElevatedSandboxUser`].
    ///   Hive always selects the sandbox-account token mode for Step 3; the
    ///   base non-elevated restricted-token mode from #395 is not selectable
    ///   through this adapter.
    pub fn from_policy(policy: &SandboxPolicy, cwd: &Path) -> Result<Self, ResolveError> {
        if !is_windows_absolute(cwd) {
            return Err(ResolveError::CwdNotAbsolute {
                cwd: cwd.to_path_buf(),
            });
        }

        let writable_roots = policy
            .writable_roots()
            .iter()
            .map(|root| WindowsWritableRoot {
                root: root.clone(),
                read_only_subpaths: Vec::new(),
            })
            .collect();

        let mut readonly_roots: Vec<PathBuf> = policy.readonly_roots().to_vec();
        let hook_config_dir = policy.hook_config_dir().to_path_buf();
        // `SandboxPolicy::build` only guards writable roots against overlapping
        // `hook_config_dir` (see policy.rs); it does not guard `readonly_roots`
        // against already containing it. Dedupe here so a caller that happens
        // to pass the hook/config directory as one of its readonly roots does
        // not end up with it listed twice in the resolved view.
        if !readonly_roots.contains(&hook_config_dir) {
            readonly_roots.push(hook_config_dir);
        }

        Ok(Self {
            writable_roots,
            readonly_roots,
            deny_read_paths: Vec::new(),
            network: policy.network().clone(),
            token_mode: WindowsSandboxTokenMode::ElevatedSandboxUser,
        })
    }

    /// `true` for [`NetworkPolicy::DenyAll`]: the resolved policy wants
    /// network confinement applied. Step 3 does not yet apply it (WFP is Step
    /// 4); see `windows.rs`'s existing network-refusal guards, which this
    /// accessor does not change.
    pub fn should_apply_network_block(&self) -> bool {
        matches!(self.network, NetworkPolicy::DenyAll)
    }

    pub fn network_policy(&self) -> &NetworkPolicy {
        &self.network
    }

    /// Every directory the sandboxed task may read: the caller's read-only
    /// roots plus the always-readable hook/config directory.
    pub fn readonly_roots(&self) -> &[PathBuf] {
        &self.readonly_roots
    }

    pub fn writable_roots(&self) -> &[WindowsWritableRoot] {
        &self.writable_roots
    }

    pub fn uses_write_capabilities(&self) -> bool {
        !self.writable_roots.is_empty()
    }

    /// Paths that should receive a deny-read ACE (fed to
    /// `codex_windows_sandbox::deny_read_acl` once wired). Always empty this
    /// wave; see [`Self::from_policy`]'s doc.
    pub fn deny_read_paths(&self) -> &[PathBuf] {
        &self.deny_read_paths
    }

    pub fn token_mode(&self) -> WindowsSandboxTokenMode {
        self.token_mode
    }
}

/// True for a drive-absolute (`C:\...`, `C:/...`) or UNC/device
/// (`\\server\share\...`, `\\?\C:\...`) Windows path. Deliberately NOT
/// `Path::is_absolute()`: this crate's tests run on Linux CI (see the module
/// doc), and `Path::is_absolute()` uses HOST path conventions, so on a Unix
/// host it evaluates `C:\workspace` as relative (no leading `/`), which would
/// make [`ResolvedWindowsSandboxPermissions::from_policy`] reject every
/// Windows-shaped test path when run on this crate's own Linux CI job. Parsed
/// with explicit `\`/`/` handling instead, same technique and same rationale
/// as `windows_plan::is_fully_qualified_program` and `windows_plan::parent_dir`
/// (see that module's doc for the `Path::parent()` bug this pattern avoids).
fn is_windows_absolute(path: &Path) -> bool {
    let raw = path.to_string_lossy();
    let bytes = raw.as_bytes();
    let is_sep = |b: u8| b == b'\\' || b == b'/';
    if bytes.len() >= 2 && is_sep(bytes[0]) && is_sep(bytes[1]) {
        // UNC or device path.
        return true;
    }
    // Drive-absolute: drive letter, `:`, then a separator.
    bytes.len() >= 3 && bytes[0].is_ascii_alphabetic() && bytes[1] == b':' && is_sep(bytes[2])
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::policy::SandboxPolicy;
    use pretty_assertions::assert_eq;

    fn policy_with(
        writable_roots: Vec<PathBuf>,
        readonly_roots: Vec<PathBuf>,
        hook_config_dir: PathBuf,
        network: NetworkPolicy,
    ) -> SandboxPolicy {
        SandboxPolicy::build(writable_roots, readonly_roots, hook_config_dir, network)
            .expect("valid policy")
    }

    #[test]
    fn from_policy_maps_writable_roots_one_to_one() {
        let policy = policy_with(
            vec![PathBuf::from(r"C:\workspace")],
            vec![],
            PathBuf::from(r"C:\hive\hooks"),
            NetworkPolicy::DenyAll,
        );

        let resolved =
            ResolvedWindowsSandboxPermissions::from_policy(&policy, Path::new(r"C:\workspace"))
                .expect("resolves");

        assert_eq!(
            resolved.writable_roots(),
            &[WindowsWritableRoot {
                root: PathBuf::from(r"C:\workspace"),
                read_only_subpaths: Vec::new(),
            }]
        );
        assert!(resolved.uses_write_capabilities());
    }

    #[test]
    fn from_policy_readonly_roots_include_hook_config_dir() {
        let policy = policy_with(
            vec![],
            vec![PathBuf::from(r"C:\usr\share")],
            PathBuf::from(r"C:\hive\hooks"),
            NetworkPolicy::DenyAll,
        );

        let resolved =
            ResolvedWindowsSandboxPermissions::from_policy(&policy, Path::new(r"C:\workspace"))
                .expect("resolves");

        assert_eq!(
            resolved.readonly_roots(),
            &[
                PathBuf::from(r"C:\usr\share"),
                PathBuf::from(r"C:\hive\hooks")
            ]
        );
        assert!(!resolved.uses_write_capabilities());
    }

    #[test]
    fn from_policy_carries_deny_all_network_and_requests_block() {
        let policy = policy_with(
            vec![],
            vec![],
            PathBuf::from(r"C:\hive\hooks"),
            NetworkPolicy::DenyAll,
        );

        let resolved =
            ResolvedWindowsSandboxPermissions::from_policy(&policy, Path::new(r"C:\workspace"))
                .expect("resolves");

        assert_eq!(resolved.network_policy(), &NetworkPolicy::DenyAll);
        assert!(resolved.should_apply_network_block());
    }

    #[test]
    fn from_policy_carries_allow_hosts_network_without_requesting_block() {
        let hosts = vec!["api.openrouter.ai".to_string()];
        let policy = policy_with(
            vec![],
            vec![],
            PathBuf::from(r"C:\hive\hooks"),
            NetworkPolicy::AllowHosts(hosts.clone()),
        );

        let resolved =
            ResolvedWindowsSandboxPermissions::from_policy(&policy, Path::new(r"C:\workspace"))
                .expect("resolves");

        assert_eq!(resolved.network_policy(), &NetworkPolicy::AllowHosts(hosts));
        assert!(!resolved.should_apply_network_block());
    }

    #[test]
    fn from_policy_deny_read_paths_are_empty_until_a_secret_path_source_exists() {
        let policy = policy_with(
            vec![],
            vec![],
            PathBuf::from(r"C:\hive\hooks"),
            NetworkPolicy::DenyAll,
        );

        let resolved =
            ResolvedWindowsSandboxPermissions::from_policy(&policy, Path::new(r"C:\workspace"))
                .expect("resolves");

        assert!(resolved.deny_read_paths().is_empty());
    }

    #[test]
    fn from_policy_always_selects_elevated_sandbox_user_token_mode() {
        let policy = policy_with(
            vec![PathBuf::from(r"C:\workspace")],
            vec![],
            PathBuf::from(r"C:\hive\hooks"),
            NetworkPolicy::DenyAll,
        );

        let resolved =
            ResolvedWindowsSandboxPermissions::from_policy(&policy, Path::new(r"C:\workspace"))
                .expect("resolves");

        assert_eq!(
            resolved.token_mode(),
            WindowsSandboxTokenMode::ElevatedSandboxUser
        );
    }

    #[test]
    fn from_policy_empty_readonly_roots_yields_only_hook_config_dir() {
        let hook_dir = PathBuf::from(r"C:\hive\hooks");
        let policy = policy_with(vec![], vec![], hook_dir.clone(), NetworkPolicy::DenyAll);

        let resolved =
            ResolvedWindowsSandboxPermissions::from_policy(&policy, Path::new(r"C:\workspace"))
                .expect("resolves");

        assert_eq!(resolved.readonly_roots(), &[hook_dir]);
    }

    #[test]
    fn from_policy_dedupes_hook_config_dir_already_present_in_readonly_roots() {
        let hook_dir = PathBuf::from(r"C:\hive\hooks");
        let policy = policy_with(
            vec![],
            vec![hook_dir.clone()],
            hook_dir.clone(),
            NetworkPolicy::DenyAll,
        );

        let resolved =
            ResolvedWindowsSandboxPermissions::from_policy(&policy, Path::new(r"C:\workspace"))
                .expect("resolves");

        assert_eq!(
            resolved.readonly_roots(),
            &[hook_dir],
            "hook_config_dir must not be duplicated when the caller already listed it as a readonly root"
        );
    }

    #[test]
    fn from_policy_accepts_unc_cwd() {
        let policy = policy_with(
            vec![],
            vec![],
            PathBuf::from(r"C:\hive\hooks"),
            NetworkPolicy::DenyAll,
        );

        let resolved = ResolvedWindowsSandboxPermissions::from_policy(
            &policy,
            Path::new(r"\\server\share\work"),
        );

        assert!(
            resolved.is_ok(),
            "a UNC path is a legitimately absolute Windows cwd and must be accepted: {resolved:?}"
        );
    }

    #[test]
    fn from_policy_accepts_forward_slash_drive_absolute_cwd() {
        let policy = policy_with(
            vec![],
            vec![],
            PathBuf::from(r"C:\hive\hooks"),
            NetworkPolicy::DenyAll,
        );

        let resolved =
            ResolvedWindowsSandboxPermissions::from_policy(&policy, Path::new("C:/workspace"));

        assert!(
            resolved.is_ok(),
            "a forward-slash drive-absolute path (C:/...) is a legitimately absolute Windows cwd \
             and must be accepted: {resolved:?}"
        );
    }

    #[test]
    fn from_policy_rejects_relative_cwd() {
        let policy = policy_with(
            vec![],
            vec![],
            PathBuf::from(r"C:\hive\hooks"),
            NetworkPolicy::DenyAll,
        );

        let err = ResolvedWindowsSandboxPermissions::from_policy(
            &policy,
            Path::new(r"relative\workspace"),
        )
        .expect_err("relative cwd must be rejected");

        assert_eq!(
            err,
            ResolveError::CwdNotAbsolute {
                cwd: PathBuf::from(r"relative\workspace")
            }
        );
    }
}
