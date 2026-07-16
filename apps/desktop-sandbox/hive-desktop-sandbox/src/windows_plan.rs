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
    /// `true` for both [`NetworkPolicy`] variants: a Windows Firewall
    /// deny-outbound-by-default rule, with [`firewall_allow_outbound_hosts`]
    /// carrying the per-host exceptions for `AllowHosts`. Blueprint Step
    /// 4.4 (#308/#311) computes this plan and the rule text
    /// ([`allow_hosts_firewall_script`]) but does not call the Firewall API
    /// -- `windows::launch` refuses to run at all regardless of network
    /// policy (see its module doc), so nothing here is ever actually
    /// applied on a real Windows host today. Marked explicitly untrusted:
    /// this is codegen only, verified by pure unit tests on Linux CI, never
    /// exercised against the real `netsh`/WFP surface.
    ///
    /// [`firewall_allow_outbound_hosts`]: WindowsConfinementPlan::firewall_allow_outbound_hosts
    pub firewall_deny_outbound: bool,

    /// Per-host outbound allow exceptions for [`NetworkPolicy::AllowHosts`],
    /// layered on top of `firewall_deny_outbound`. Empty for
    /// [`NetworkPolicy::DenyAll`] (nothing to allow).
    pub firewall_allow_outbound_hosts: Vec<String>,
}

impl WindowsConfinementPlan {
    pub fn for_policy(policy: &SandboxPolicy) -> Self {
        let firewall_allow_outbound_hosts = match policy.network() {
            NetworkPolicy::DenyAll => Vec::new(),
            NetworkPolicy::AllowHosts(hosts) => hosts.clone(),
        };
        Self {
            acl_deny_write_parent_dir: parent_dir(policy.hook_config_dir()),
            protect_dacl_from_inheritance: true,
            job_object_kill_on_close: true,
            firewall_deny_outbound: true,
            firewall_allow_outbound_hosts,
        }
    }
}

/// Rule-name prefix for every generated rule, so a caller (or a human
/// reading `netsh advfirewall firewall show rule name=all`) can find and
/// remove every rule this crate ever generated for one task by prefix.
const RULE_NAME_PREFIX: &str = "Hive-Sandbox";

/// Generates the `netsh advfirewall` command lines implementing `plan`,
/// scoped to `program_path` (the sandboxed task's own executable) via
/// `netsh`'s `program=` parameter so the rules only ever affect that one
/// process, never the whole host. Pure text generation -- see the module
/// doc and `firewall_deny_outbound`'s doc comment for why this is never
/// applied by this crate. `task_id` scopes the rule *names* so concurrent
/// sandboxed tasks (if this is ever wired to run more than one at a time)
/// don't collide or shadow each other's rules.
///
/// Known incomplete design, not yet a working allowlist: Windows Firewall
/// evaluates `Block` rules ahead of `Allow` rules regardless of
/// specificity, so the single `action=block` rule below always wins over
/// every per-host `action=allow` rule that follows it -- the net effect if
/// this were ever applied is a strict deny-all, not the requested
/// `AllowHosts`. `netsh` has no "allow overrides block" priority to ask
/// for; a real allowlist needs either a single block rule built from an
/// inverted (everything-except-the-allowed-hosts) IP range, or dropping
/// `netsh` for the Windows Filtering Platform (WFP) directly. Tracked as a
/// follow-up (see the crate's issue tracker); do not wire this to a real
/// `netsh`/WFP call without fixing the precedence problem first, and note
/// that `windows::launch` refuses to run at all today regardless of
/// network policy, so nothing calls this for a live effect yet either way.
pub fn allow_hosts_firewall_script(
    plan: &WindowsConfinementPlan,
    task_id: &str,
    program_path: &str,
) -> Vec<String> {
    let mut lines = Vec::with_capacity(1 + plan.firewall_allow_outbound_hosts.len());
    if plan.firewall_deny_outbound {
        lines.push(format!(
            "netsh advfirewall firewall add rule name=\"{RULE_NAME_PREFIX}-{task_id}-deny\" \
             dir=out action=block program=\"{program_path}\" enable=yes"
        ));
    }
    for host in &plan.firewall_allow_outbound_hosts {
        lines.push(format!(
            "netsh advfirewall firewall add rule name=\"{RULE_NAME_PREFIX}-{task_id}-allow-{host}\" \
             dir=out action=allow program=\"{program_path}\" remoteip={host} enable=yes"
        ));
    }
    lines
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
        Some(idx) if idx > 0 => {
            let parent = &trimmed[..idx];
            // "C:" alone means "current directory on drive C" to Win32,
            // not the drive root; keep the separator so the ACE target is
            // the unambiguous root "C:\", not a relative-to-cwd path.
            if is_bare_drive_letter(parent) {
                PathBuf::from(format!("{parent}\\"))
            } else {
                PathBuf::from(parent)
            }
        }
        // No separator, or only a drive-root separator (e.g. "C:\"): there
        // is no parent to ascend to. Falling back to the dir itself keeps
        // the ACE somewhere rather than producing an empty path, and
        // `protect_dacl_from_inheritance` still applies to it.
        _ => hook_config_dir.to_path_buf(),
    }
}

/// True for a bare drive letter with no trailing separator, e.g. `"C:"`.
fn is_bare_drive_letter(s: &str) -> bool {
    let bytes = s.as_bytes();
    bytes.len() == 2 && bytes[0].is_ascii_alphabetic() && bytes[1] == b':'
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

        let plan = WindowsConfinementPlan::for_policy(&policy);
        assert!(plan.firewall_deny_outbound);
        assert!(
            plan.firewall_allow_outbound_hosts.is_empty(),
            "DenyAll must not carry any allow exceptions"
        );
    }

    #[test]
    fn plan_requests_deny_outbound_and_per_host_exceptions_for_allow_hosts_network() {
        let policy = SandboxPolicy::build(
            vec![],
            vec![],
            PathBuf::from(r"C:\Users\agent\AppData\Hive\hooks"),
            NetworkPolicy::AllowHosts(vec!["api.openrouter.ai".to_string()]),
        )
        .expect("valid policy");

        let plan = WindowsConfinementPlan::for_policy(&policy);
        // Blueprint Step 4.4 (#308/#311): deny-outbound-by-default now
        // applies to AllowHosts too, not just DenyAll -- the exceptions
        // below are what makes it an *allow*list rather than a full block.
        assert!(plan.firewall_deny_outbound);
        assert_eq!(
            plan.firewall_allow_outbound_hosts,
            vec!["api.openrouter.ai".to_string()]
        );
    }

    #[test]
    fn allow_hosts_firewall_script_orders_deny_rule_before_allow_exceptions() {
        let policy = SandboxPolicy::build(
            vec![],
            vec![],
            PathBuf::from(r"C:\Users\agent\AppData\Hive\hooks"),
            NetworkPolicy::AllowHosts(vec![
                "api.openrouter.ai".to_string(),
                "example.com".to_string(),
            ]),
        )
        .expect("valid policy");
        let plan = WindowsConfinementPlan::for_policy(&policy);

        let lines = allow_hosts_firewall_script(&plan, "task-123", r"C:\hive\sandboxed-task.exe");
        assert_eq!(lines.len(), 3, "one deny rule plus one allow rule per host");
        assert!(lines[0].contains("action=block"));
        assert!(lines[0].contains("task-123"));
        assert!(lines[1].contains("action=allow") && lines[1].contains("api.openrouter.ai"));
        assert!(lines[2].contains("action=allow") && lines[2].contains("example.com"));
        for line in &lines {
            assert!(
                line.contains(r#"program="C:\hive\sandboxed-task.exe""#),
                "every rule must be scoped to the sandboxed task's own executable, not the whole host: {line}"
            );
        }
    }

    #[test]
    fn allow_hosts_firewall_script_for_deny_all_is_just_the_deny_rule() {
        let policy = SandboxPolicy::build(
            vec![],
            vec![],
            PathBuf::from(r"C:\Users\agent\AppData\Hive\hooks"),
            NetworkPolicy::DenyAll,
        )
        .expect("valid policy");
        let plan = WindowsConfinementPlan::for_policy(&policy);

        let lines = allow_hosts_firewall_script(&plan, "task-456", r"C:\hive\sandboxed-task.exe");
        assert_eq!(lines.len(), 1);
        assert!(lines[0].contains("action=block"));
        assert!(lines[0].contains(r#"program="C:\hive\sandboxed-task.exe""#));
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

    #[test]
    fn plan_for_hook_config_dir_one_level_below_drive_root_keeps_root_separator() {
        let policy = SandboxPolicy::build(
            vec![],
            vec![],
            PathBuf::from(r"C:\hooks"),
            NetworkPolicy::DenyAll,
        )
        .expect("valid policy");

        let plan = WindowsConfinementPlan::for_policy(&policy);
        // Must be "C:\" (the drive root), not "C:" (Win32's "current
        // directory on drive C", a different and non-deterministic path).
        assert_eq!(plan.acl_deny_write_parent_dir, PathBuf::from(r"C:\"));
    }
}
