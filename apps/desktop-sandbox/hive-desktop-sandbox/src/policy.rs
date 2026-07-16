//! Sandbox policy model shared by the Linux and Windows desktop backends.
//!
//! [`SandboxPolicy`] is the single source of truth for what a sandboxed
//! agent task may touch. Construction enforces the CBSE mitigation from
//! security spike #307 (conditions 2 and 3, see the spike report referenced
//! in `VENDORING.md`): the hook/config directory can never end up inside the
//! writable set, even if a caller's writable roots would otherwise cover it.
//! This is checked once, here, so every backend (Linux, Windows) and every
//! future caller of this crate inherits the guarantee for free instead of
//! re-deriving it per call site.

use std::path::{Path, PathBuf};

/// Network access for a sandboxed task.
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum NetworkPolicy {
    /// No network access. Enforced on Linux via a new network namespace
    /// (bubblewrap `--unshare-net`) and on Windows via a deny-outbound
    /// Windows Firewall rule scoped to the sandboxed process (elevated
    /// variant only).
    DenyAll,
    /// Per-host allow-listing, matching the egress single-source-of-truth
    /// shape from #308/#319. The shape is stable so a policy can round-trip
    /// through the control-plane egress API today; enforcement is not yet
    /// implemented by this crate's launch path (both backends return
    /// [`crate::LaunchError::AllowHostsNotYetImplemented`]). See
    /// VENDORING.md "Open risks".
    AllowHosts(Vec<String>),
}

/// What a sandboxed task may read, write, and reach over the network.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct SandboxPolicy {
    writable_roots: Vec<PathBuf>,
    readonly_roots: Vec<PathBuf>,
    hook_config_dir: PathBuf,
    network: NetworkPolicy,
}

/// Rejected policy construction. There is deliberately no variant for
/// "silently drop the offending root" — a policy that cannot be constructed
/// safely must fail loudly rather than launch with a weaker guarantee than
/// the caller asked for.
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum PolicyError {
    /// A writable root contains, or is contained by, the hook/config
    /// directory. Spike #307 rows 10/13 showed this exact overlap is the
    /// write-then-unsandboxed-exec CBSE class.
    HookConfigDirOverlapsWritableRoot { writable_root: PathBuf },
}

impl std::fmt::Display for PolicyError {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        match self {
            PolicyError::HookConfigDirOverlapsWritableRoot { writable_root } => write!(
                f,
                "writable root {} overlaps the hook/config directory; the hook/config \
                 directory must never be writable by the sandboxed task",
                writable_root.display()
            ),
        }
    }
}

impl std::error::Error for PolicyError {}

impl SandboxPolicy {
    /// Builds a policy, rejecting any writable root that overlaps
    /// `hook_config_dir` in either direction (the root contains the hook
    /// dir, or the hook dir contains the root).
    pub fn build(
        writable_roots: Vec<PathBuf>,
        readonly_roots: Vec<PathBuf>,
        hook_config_dir: PathBuf,
        network: NetworkPolicy,
    ) -> Result<Self, PolicyError> {
        for writable_root in &writable_roots {
            if paths_overlap(writable_root, &hook_config_dir) {
                return Err(PolicyError::HookConfigDirOverlapsWritableRoot {
                    writable_root: writable_root.clone(),
                });
            }
        }
        Ok(Self {
            writable_roots,
            readonly_roots,
            hook_config_dir,
            network,
        })
    }

    pub fn writable_roots(&self) -> &[PathBuf] {
        &self.writable_roots
    }

    /// Read-only roots, not including the hook/config directory (see
    /// [`SandboxPolicy::hook_config_dir`], which is always read-only and is
    /// kept separate so backends can apply it as a distinct, unremovable
    /// rule rather than one entry among many).
    pub fn readonly_roots(&self) -> &[PathBuf] {
        &self.readonly_roots
    }

    /// Directory holding hook, notify, and lifecycle configuration. Always
    /// read-only bind-mounted (Linux) or ACL-denied for write (Windows),
    /// regardless of what the caller passed as writable roots: see
    /// [`SandboxPolicy::build`].
    pub fn hook_config_dir(&self) -> &Path {
        &self.hook_config_dir
    }

    pub fn network(&self) -> &NetworkPolicy {
        &self.network
    }
}

fn paths_overlap(a: &Path, b: &Path) -> bool {
    a.starts_with(b) || b.starts_with(a)
}

#[cfg(test)]
mod tests {
    use super::*;
    use pretty_assertions::assert_eq;

    #[test]
    fn build_rejects_writable_root_equal_to_hook_config_dir() {
        let hook_dir = PathBuf::from("/home/agent/.hive/hooks");
        let err = SandboxPolicy::build(
            vec![hook_dir.clone()],
            vec![],
            hook_dir.clone(),
            NetworkPolicy::DenyAll,
        )
        .expect_err("hook config dir must never be constructible as writable");

        assert_eq!(
            err,
            PolicyError::HookConfigDirOverlapsWritableRoot {
                writable_root: hook_dir
            }
        );
    }

    #[test]
    fn build_rejects_writable_root_that_is_ancestor_of_hook_config_dir() {
        let workspace = PathBuf::from("/home/agent/workspace");
        let hook_dir = PathBuf::from("/home/agent/workspace/.hive/hooks");
        let err = SandboxPolicy::build(
            vec![workspace.clone()],
            vec![],
            hook_dir,
            NetworkPolicy::DenyAll,
        )
        .expect_err("a writable ancestor of the hook config dir must be rejected");

        assert_eq!(
            err,
            PolicyError::HookConfigDirOverlapsWritableRoot {
                writable_root: workspace
            }
        );
    }

    #[test]
    fn build_rejects_writable_root_nested_inside_hook_config_dir() {
        let hook_dir = PathBuf::from("/home/agent/.hive/hooks");
        let nested = PathBuf::from("/home/agent/.hive/hooks/scripts");
        let err = SandboxPolicy::build(
            vec![nested.clone()],
            vec![],
            hook_dir,
            NetworkPolicy::DenyAll,
        )
        .expect_err("a writable root nested inside the hook config dir must be rejected");

        assert_eq!(
            err,
            PolicyError::HookConfigDirOverlapsWritableRoot {
                writable_root: nested
            }
        );
    }

    #[test]
    fn build_accepts_disjoint_writable_root_and_hook_config_dir() {
        let workspace = PathBuf::from("/home/agent/workspace");
        let hook_dir = PathBuf::from("/home/agent/.hive/hooks");
        let policy = SandboxPolicy::build(
            vec![workspace.clone()],
            vec![PathBuf::from("/usr")],
            hook_dir.clone(),
            NetworkPolicy::DenyAll,
        )
        .expect("disjoint roots must be accepted");

        assert_eq!(policy.writable_roots(), &[workspace]);
        assert_eq!(policy.readonly_roots(), &[PathBuf::from("/usr")]);
        assert_eq!(policy.hook_config_dir(), hook_dir);
        assert_eq!(policy.network(), &NetworkPolicy::DenyAll);
    }

    #[test]
    fn build_accepts_allow_hosts_shape_even_though_enforcement_is_deferred() {
        // The egress-SSOT shape must round-trip today; only the launch path
        // (linux::launch / windows::launch) rejects it as not yet enforced.
        let policy = SandboxPolicy::build(
            vec![PathBuf::from("/home/agent/workspace")],
            vec![],
            PathBuf::from("/home/agent/.hive/hooks"),
            NetworkPolicy::AllowHosts(vec!["api.openrouter.ai".to_string()]),
        )
        .expect("AllowHosts must be constructible");

        assert_eq!(
            policy.network(),
            &NetworkPolicy::AllowHosts(vec!["api.openrouter.ai".to_string()])
        );
    }
}
