//! Linux desktop sandbox backend: bubblewrap (bundled static binary, ~1 MB;
//! see the vendored `codex-bwrap` crate and `vendor/bubblewrap/`) plus a
//! Landlock ruleset and a seccomp-BPF denylist applied to the bwrap process
//! itself, before bwrap creates its own namespaces. This is defense in
//! depth: Landlock and seccomp are per-task kernel state inherited across
//! bwrap's own `execve`, so they hold even if bwrap's own mount-namespace
//! handling has a bug.
//!
//! MANDATORY per security spike #307 (implementation condition 2): the
//! hook/config directory is always `--ro-bind`-mounted AND covered by a
//! read-only Landlock rule, regardless of the rest of the policy. See
//! `policy.rs` for where that invariant is actually enforced (at
//! [`SandboxPolicy::build`], not here — by the time a `SandboxPolicy`
//! reaches this module, the invariant already holds).
//!
//! Ubuntu 24.04 ships `kernel.apparmor_restrict_unprivileged_userns=1`,
//! which blocks unprivileged bubblewrap's `--unshare-user`. See
//! `assets/apparmor/hive-bwrap-userns` and its install doc in `README.md`.

use crate::policy::NetworkPolicy;
use crate::{LaunchError, SandboxPolicy};
use landlock::{
    ABI, Access, AccessFs, CompatLevel, Compatible, PathBeneath, PathFd, Ruleset, RulesetAttr,
    RulesetCreatedAttr,
};
use seccompiler::{BpfProgram, SeccompAction, SeccompFilter};
use std::convert::TryInto;
use std::os::unix::process::CommandExt;
use std::path::{Path, PathBuf};
use std::process::{Child, Command};

/// Syscalls denied unconditionally for every sandboxed task, on top of
/// bubblewrap's own namespace isolation. None of these are needed by
/// ordinary shell/build/test workloads, and each is a known cross-process
/// snooping or sandbox-escape primitive.
const DENIED_SYSCALLS: &[i64] = &[
    libc::SYS_ptrace,
    libc::SYS_process_vm_readv,
    libc::SYS_process_vm_writev,
    libc::SYS_kexec_load,
    libc::SYS_init_module,
    libc::SYS_finit_module,
    libc::SYS_delete_module,
];

pub(crate) fn launch(
    policy: &SandboxPolicy,
    command: &[String],
    cwd: &Path,
) -> Result<Child, LaunchError> {
    if matches!(policy.network(), NetworkPolicy::AllowHosts(_)) {
        return Err(LaunchError::AllowHostsNotYetImplemented);
    }

    let bwrap_path = locate_bwrap_binary();
    let argv = build_bwrap_argv(policy, command, cwd);
    let policy_for_child = policy.clone();

    let mut cmd = Command::new(bwrap_path);
    cmd.args(&argv);
    // SAFETY: the closure only calls into `codex_process_hardening`,
    // `landlock`, and `seccompiler`, all of which perform their own
    // syscalls directly and do not allocate in a way that is unsafe
    // between fork and exec.
    unsafe {
        cmd.pre_exec(move || {
            codex_process_hardening::pre_main_hardening();
            apply_landlock_ruleset(&policy_for_child)
                .map_err(|err| std::io::Error::other(format!("landlock: {err}")))?;
            apply_seccomp_denylist()
                .map_err(|err| std::io::Error::other(format!("seccomp: {err}")))?;
            Ok(())
        });
    }
    cmd.spawn().map_err(LaunchError::from)
}

/// Locates the bundled static bwrap binary. Priority: `HIVE_BWRAP_PATH`
/// override, then a `bwrap` binary shipped next to the running executable
/// (the desktop app's packaging step places it there from the
/// `codex-bwrap` crate's `[[bin]] name = "bwrap"` build output), then
/// whatever `bwrap` resolves to on `PATH`.
fn locate_bwrap_binary() -> PathBuf {
    if let Ok(path) = std::env::var("HIVE_BWRAP_PATH") {
        let path = PathBuf::from(path);
        if path.is_file() {
            return path;
        }
    }
    if let Ok(exe) = std::env::current_exe()
        && let Some(dir) = exe.parent()
    {
        let bundled = dir.join("bwrap");
        if bundled.is_file() {
            return bundled;
        }
    }
    PathBuf::from("bwrap")
}

fn build_bwrap_argv(policy: &SandboxPolicy, command: &[String], cwd: &Path) -> Vec<String> {
    let mut argv: Vec<String> = vec![
        "--unshare-user".to_string(),
        "--unshare-pid".to_string(),
        "--unshare-ipc".to_string(),
        "--unshare-uts".to_string(),
        "--die-with-parent".to_string(),
        "--proc".to_string(),
        "/proc".to_string(),
        "--dev".to_string(),
        "/dev".to_string(),
    ];

    if matches!(policy.network(), NetworkPolicy::DenyAll) {
        argv.push("--unshare-net".to_string());
    }

    // Hook/config dir first so a caller-supplied readonly root can never
    // shadow it: bwrap applies bind rules in argv order and the last rule
    // for a given destination wins.
    for path in read_only_paths(policy) {
        let path_str = path.to_string_lossy().into_owned();
        argv.push("--ro-bind".to_string());
        argv.push(path_str.clone());
        argv.push(path_str);
    }

    for path in policy.writable_roots() {
        let path_str = path.to_string_lossy().into_owned();
        argv.push("--bind".to_string());
        argv.push(path_str.clone());
        argv.push(path_str);
    }

    argv.push("--chdir".to_string());
    argv.push(cwd.to_string_lossy().into_owned());
    argv.push("--".to_string());
    argv.extend(command.iter().cloned());
    argv
}

/// The hook/config dir plus the caller's readonly roots, hook dir first.
fn read_only_paths(policy: &SandboxPolicy) -> impl Iterator<Item = &Path> {
    std::iter::once(policy.hook_config_dir())
        .chain(policy.readonly_roots().iter().map(PathBuf::as_path))
}

fn apply_landlock_ruleset(policy: &SandboxPolicy) -> Result<(), String> {
    let abi = ABI::V5;
    let access_all = AccessFs::from_all(abi);
    let access_read = AccessFs::from_read(abi);

    // CompatLevel::HardRequirement, not the crate's BestEffort default:
    // Linux is the demo-live confinement path, so a kernel that can't
    // enforce the ruleset we ask for must fail loudly here rather than
    // silently return `RulesetStatus::NotEnforced`/`PartiallyEnforced` and
    // let the sandboxed process run unconfined.
    let mut ruleset = Ruleset::default()
        .set_compatibility(CompatLevel::HardRequirement)
        .handle_access(access_all)
        .map_err(|err| format!("{err:?}"))?
        .create()
        .map_err(|err| format!("{err:?}"))?;

    for path in read_only_paths(policy) {
        let fd = PathFd::new(path).map_err(|err| format!("open {}: {err:?}", path.display()))?;
        ruleset = ruleset
            .add_rule(PathBeneath::new(fd, access_read))
            .map_err(|err| format!("{err:?}"))?;
    }

    for path in policy.writable_roots() {
        let fd = PathFd::new(path).map_err(|err| format!("open {}: {err:?}", path.display()))?;
        ruleset = ruleset
            .add_rule(PathBeneath::new(fd, access_all))
            .map_err(|err| format!("{err:?}"))?;
    }

    ruleset.restrict_self().map_err(|err| format!("{err:?}"))?;
    Ok(())
}

fn apply_seccomp_denylist() -> Result<(), String> {
    let rules = DENIED_SYSCALLS
        .iter()
        .map(|&syscall| (syscall, Vec::new()))
        .collect();

    let arch = std::env::consts::ARCH.try_into().map_err(|_| {
        format!(
            "unsupported architecture for seccomp filter: {}",
            std::env::consts::ARCH
        )
    })?;

    let filter: BpfProgram = SeccompFilter::new(
        rules,
        SeccompAction::Allow,
        SeccompAction::Errno(libc::EPERM as u32),
        arch,
    )
    .map_err(|err| format!("{err:?}"))?
    .try_into()
    .map_err(|err| format!("{err:?}"))?;

    seccompiler::apply_filter(&filter).map_err(|err| format!("{err:?}"))
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::policy::SandboxPolicy;
    use pretty_assertions::assert_eq;

    fn policy(writable: Vec<PathBuf>, readonly: Vec<PathBuf>, hook: PathBuf) -> SandboxPolicy {
        SandboxPolicy::build(writable, readonly, hook, NetworkPolicy::DenyAll)
            .expect("valid policy")
    }

    #[test]
    fn hook_config_dir_is_always_ro_bound_ahead_of_readonly_roots() {
        let hook = PathBuf::from("/home/agent/.hive/hooks");
        let p = policy(vec![], vec![PathBuf::from("/usr")], hook.clone());
        let argv = build_bwrap_argv(&p, &["true".to_string()], Path::new("/tmp"));

        let hook_str = hook.to_string_lossy().into_owned();
        let hook_idx = argv
            .windows(2)
            .position(|w| w[0] == "--ro-bind" && w[1] == hook_str)
            .expect("hook config dir must be ro-bound");
        let usr_idx = argv
            .windows(2)
            .position(|w| w[0] == "--ro-bind" && w[1] == "/usr")
            .expect("caller readonly root must be ro-bound");
        assert!(
            hook_idx < usr_idx,
            "hook config dir must be bound before other readonly roots"
        );
    }

    #[test]
    fn deny_all_network_adds_unshare_net() {
        let p = policy(vec![], vec![], PathBuf::from("/home/agent/.hive/hooks"));
        let argv = build_bwrap_argv(&p, &["true".to_string()], Path::new("/tmp"));
        assert!(argv.contains(&"--unshare-net".to_string()));
    }

    #[test]
    fn writable_root_is_bind_not_ro_bind() {
        let workspace = PathBuf::from("/home/agent/workspace");
        let p = policy(
            vec![workspace.clone()],
            vec![],
            PathBuf::from("/home/agent/.hive/hooks"),
        );
        let argv = build_bwrap_argv(&p, &["true".to_string()], Path::new("/tmp"));

        let workspace_str = workspace.to_string_lossy().into_owned();
        assert!(
            argv.windows(2)
                .any(|w| w[0] == "--bind" && w[1] == workspace_str)
        );
        assert!(
            !argv
                .windows(2)
                .any(|w| w[0] == "--ro-bind" && w[1] == workspace_str)
        );
    }

    #[test]
    fn command_is_appended_after_separator() {
        let p = policy(vec![], vec![], PathBuf::from("/home/agent/.hive/hooks"));
        let argv = build_bwrap_argv(
            &p,
            &["echo".to_string(), "hi".to_string()],
            Path::new("/tmp"),
        );
        let sep = argv
            .iter()
            .position(|a| a == "--")
            .expect("separator present");
        assert_eq!(&argv[sep + 1..], &["echo".to_string(), "hi".to_string()]);
    }

    #[test]
    fn allow_hosts_policy_is_rejected_at_launch_not_silently_downgraded() {
        let p = SandboxPolicy::build(
            vec![],
            vec![],
            PathBuf::from("/home/agent/.hive/hooks"),
            NetworkPolicy::AllowHosts(vec!["example.com".to_string()]),
        )
        .expect("shape is constructible");

        let err = launch(&p, &["true".to_string()], Path::new("/tmp"))
            .expect_err("AllowHosts must not launch silently");
        assert!(matches!(err, LaunchError::AllowHostsNotYetImplemented));
    }

    // ponytail: calls Ruleset::restrict_self, a one-way, per-thread kernel
    // restriction (like seccomp, Landlock confines the calling thread, not
    // sibling threads already running), so it is safe to run inline here
    // rather than in a separate `tests/` process: no other test in this
    // binary performs real filesystem I/O. If a future test needs real FS
    // access and cargo's thread pool happens to reuse this OS thread, move
    // this test to its own `tests/` integration binary instead.
    #[test]
    fn landlock_hard_requirement_never_silently_no_ops() {
        let workspace = tempfile::tempdir().expect("tempdir");
        let hooks = tempfile::tempdir().expect("tempdir");
        let outside = tempfile::tempdir().expect("tempdir");
        let p = policy(
            vec![workspace.path().to_path_buf()],
            vec![],
            hooks.path().to_path_buf(),
        );

        match apply_landlock_ruleset(&p) {
            Ok(()) => {
                // CompatLevel::HardRequirement means Ok(()) here only
                // happens when the kernel actually enforced the ruleset.
                // Prove it behaviorally rather than trusting the return
                // value alone.
                let blocked = outside.path().join("blocked");
                assert!(
                    std::fs::write(&blocked, b"x").is_err(),
                    "landlock reported success but did not confine writes \
                     outside the writable roots"
                );
                let allowed = workspace.path().join("allowed");
                std::fs::write(&allowed, b"x").expect("writable root must stay writable");
            }
            Err(_) => {
                // Acceptable under CompatLevel::HardRequirement on a kernel
                // without Landlock support: a loud error, never a silent
                // no-op. That is exactly the fail-closed behavior under
                // test.
            }
        }
    }
}
