//! Windows confinement plan: the platform-independent computation of what
//! must be enforced for a [`SandboxPolicy`] on Windows. This module has no
//! Win32 dependency and compiles and runs its tests on every platform
//! (including this crate's Linux CI job), which is deliberate: the
//! enforced-defaults invariants are exactly the part of the Windows backend
//! this repository can actually verify without a Windows toolchain. The
//! Win32 calls that apply this plan live in `windows.rs`, are compiled only
//! `cfg(windows)`, and are documented in `VENDORING.md` as needing lab
//! validation.

use crate::SandboxPolicy;
use crate::policy::NetworkPolicy;
use std::path::{Path, PathBuf};

/// Windows-specific enforcement plan derived from a [`SandboxPolicy`].
/// Every field is a MUST-apply default: there is no policy input that
/// produces a plan without the directory ACL or without
/// `job_object_kill_on_close`.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct WindowsConfinementPlan {
    /// Parent directory of `hook_config_dir`. The deny-write ACE for the
    /// sandbox principal goes on THIS directory (object-inherit +
    /// container-inherit, inheritance from above disabled), not on
    /// `hook_config_dir` itself. Security spike #307 row 13 confirmed a
    /// file/dir-level ACL alone does not close the TOCTOU
    /// missing-file-create class; only the parent-directory ACE does.
    pub acl_deny_write_parent_dir: PathBuf,
    /// Always `true`. The parent directory's DACL must be protected
    /// (`PROTECTED_DACL_SECURITY_INFORMATION`) so an inherited
    /// Users-writable ACE from further up the tree cannot coexist with the
    /// new deny ACE.
    pub protect_dacl_from_inheritance: bool,
    /// Always `true`. Job Objects give process-tree containment, not
    /// filesystem protection (spike #307: "zero filesystem-CBSE protection
    /// by itself"), but killing the whole tree when the sandbox handle
    /// closes is still required so no sandboxed process outlives its
    /// workspace/ACL lifetime.
    pub job_object_kill_on_close: bool,
    /// `true` for [`NetworkPolicy::DenyAll`]. This is a signal for
    /// blueprint Step 4.4 ("desktop firewall and task portability"), which
    /// owns generating the actual Windows Firewall rule from the egress
    /// SSOT (#308/#319); this crate computes the intent but does not call
    /// the Firewall API itself. The launch path rejects
    /// [`NetworkPolicy::AllowHosts`] outright (see `windows.rs::launch`)
    /// rather than expressing "no rule yet" as a silent allow-all.
    pub firewall_deny_outbound: bool,
}

impl WindowsConfinementPlan {
    pub fn for_policy(policy: &SandboxPolicy) -> Self {
        Self {
            acl_deny_write_parent_dir: parent_dir(policy.hook_config_dir()),
            protect_dacl_from_inheritance: true,
            job_object_kill_on_close: true,
            firewall_deny_outbound: matches!(policy.network(), NetworkPolicy::DenyAll),
        }
    }
}

/// Computes the parent of a Windows-style path using explicit `\`/`/`
/// parsing rather than `std::path::Path::parent`. This module's tests are
/// required to run meaningfully on the Linux CI job (see module docs), but
/// `Path` uses the host's native separator: on a Unix host it treats `\` as
/// an ordinary filename character, not a component boundary, so
/// `Path::parent` silently gives the wrong answer for `C:\...` input when
/// this crate is checked on Linux.
fn parent_dir(hook_config_dir: &Path) -> PathBuf {
    let raw = hook_config_dir.to_string_lossy();
    let trimmed = raw.trim_end_matches(['\\', '/']);
    match trimmed.rfind(['\\', '/']) {
        Some(idx) if idx > 0 => PathBuf::from(&trimmed[..idx]),
        // No separator, or only a drive-root separator (e.g. "C:\"): there
        // is no parent to ascend to. Falling back to the dir itself keeps
        // the ACE somewhere rather than producing an empty path, and
        // `protect_dacl_from_inheritance` still applies to it.
        _ => hook_config_dir.to_path_buf(),
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::policy::SandboxPolicy;
    use pretty_assertions::assert_eq;

    #[test]
    fn plan_acls_the_parent_of_hook_config_dir_not_the_dir_itself() {
        let policy = SandboxPolicy::build(
            vec![],
            vec![],
            PathBuf::from(r"C:\Users\agent\AppData\Hive\hooks"),
            NetworkPolicy::DenyAll,
        )
        .expect("valid policy");

        let plan = WindowsConfinementPlan::for_policy(&policy);
        assert_eq!(
            plan.acl_deny_write_parent_dir,
            PathBuf::from(r"C:\Users\agent\AppData\Hive")
        );
    }

    #[test]
    fn plan_always_protects_dacl_and_sets_kill_on_close() {
        let policy = SandboxPolicy::build(
            vec![PathBuf::from(r"C:\Users\agent\workspace")],
            vec![],
            PathBuf::from(r"C:\Users\agent\AppData\Hive\hooks"),
            NetworkPolicy::DenyAll,
        )
        .expect("valid policy");

        let plan = WindowsConfinementPlan::for_policy(&policy);
        assert!(
            plan.protect_dacl_from_inheritance,
            "DACL protection must not be optional"
        );
        assert!(
            plan.job_object_kill_on_close,
            "kill-on-close must not be optional"
        );
    }

    #[test]
    fn plan_requests_deny_outbound_firewall_for_deny_all_network() {
        let policy = SandboxPolicy::build(
            vec![],
            vec![],
            PathBuf::from(r"C:\Users\agent\AppData\Hive\hooks"),
            NetworkPolicy::DenyAll,
        )
        .expect("valid policy");

        assert!(WindowsConfinementPlan::for_policy(&policy).firewall_deny_outbound);
    }

    #[test]
    fn plan_for_hook_config_dir_at_drive_root_falls_back_to_itself() {
        let policy = SandboxPolicy::build(
            vec![],
            vec![],
            PathBuf::from(r"C:\"),
            NetworkPolicy::DenyAll,
        )
        .expect("valid policy");

        let plan = WindowsConfinementPlan::for_policy(&policy);
        assert_eq!(plan.acl_deny_write_parent_dir, PathBuf::from(r"C:\"));
    }
}
