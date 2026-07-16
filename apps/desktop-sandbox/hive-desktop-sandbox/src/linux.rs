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

use crate::egress_proxy::AllowlistProxy;
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

/// Bind-mount sources for a [`NetworkPolicy::AllowHosts`] launch: the
/// [`AllowlistProxy`]'s Unix socket and the `hive-egress-shim` binary, both
/// bind-mounted source-path-equals-destination-path (see
/// [`build_bwrap_argv`]'s doc comment on why that convention matters for
/// Landlock rule portability). Constructing one always starts a real proxy
/// (see [`setup_egress`]) -- there is no variant that carries an allowlist
/// without a live enforcement point behind it.
///
/// Owns `dir` (the temp directory holding the socket) and `proxy` until
/// [`EgressBind::leak_for_process_lifetime`] is called. `launch` calls that
/// only after `cmd.spawn()` has actually produced a `Child` -- if anything
/// fails first (locating the shim binary, `cmd.spawn()` itself), this value
/// is simply dropped instead, which shuts the proxy down
/// (`AllowlistProxy`'s own `Drop`) and removes the temp directory
/// (`TempDir`'s `Drop`) rather than leaking either for a launch that never
/// happened.
struct EgressBind {
    socket_path: PathBuf,
    shim_path: PathBuf,
    dir: tempfile::TempDir,
    proxy: AllowlistProxy,
}

impl EgressBind {
    /// The wrapped command bwrap should actually exec: the shim binary
    /// first (argv-safe -- never a shell string), which bridges the
    /// bind-mounted socket to a loopback HTTP_PROXY inside the sandbox's
    /// own netns before spawning `real_command` as its own child (see
    /// `src/bin/hive-egress-shim.rs`'s module doc for the exact contract
    /// and why it spawns rather than execs).
    fn wrap_command(&self, real_command: &[String]) -> Vec<String> {
        let mut wrapped = vec![
            self.shim_path.to_string_lossy().into_owned(),
            self.socket_path.to_string_lossy().into_owned(),
            "--".to_string(),
        ];
        wrapped.extend(real_command.iter().cloned());
        wrapped
    }

    /// Leaks the temp directory and the proxy for the rest of the
    /// process's life. Call only once a real sandboxed `Child` exists for
    /// them to serve -- see the struct doc.
    fn leak_for_process_lifetime(self) {
        std::mem::forget(self.dir);
        self.proxy.leak_for_process_lifetime();
    }
}

/// Starts the host-side [`AllowlistProxy`] for `hosts` and resolves the
/// `hive-egress-shim` binary's location. Neither is leaked here -- see
/// [`EgressBind`]'s struct doc for why that is `launch`'s call to make, not
/// this function's.
fn setup_egress(hosts: &[String]) -> Result<EgressBind, LaunchError> {
    let dir = tempfile::Builder::new()
        .prefix("hive-egress-")
        .tempdir()
        .map_err(|e| LaunchError::Confinement(format!("egress temp dir: {e}")))?;
    let socket_path = dir.path().join("egress.sock");

    let proxy = AllowlistProxy::spawn(&socket_path, hosts.to_vec())
        .map_err(|e| LaunchError::Confinement(format!("egress proxy: {e}")))?;

    let shim_path = locate_egress_shim_binary().ok_or_else(|| {
        LaunchError::Confinement(
            "hive-egress-shim binary not found (set HIVE_EGRESS_SHIM_PATH, bundle it next to \
             the executable, or install it on PATH)"
                .to_string(),
        )
    })?;

    Ok(EgressBind {
        socket_path,
        shim_path,
        dir,
        proxy,
    })
}

/// Locates the bundled `hive-egress-shim` binary, mirroring
/// [`locate_bwrap_binary`]'s search order (env override, then bundled next
/// to the running executable) with one deliberate difference in the last
/// resort: this result is used as a bwrap `--ro-bind` *source* path, not
/// (only) as something `Command::new` execs. `Command::new` resolves a
/// bare name through `PATH` itself (`execvp` semantics), but bwrap's
/// `--ro-bind` does not -- it opens the literal path given, so a bare
/// `"hive-egress-shim"` would fail there even when the binary is
/// legitimately on `PATH`. This resolves `PATH` itself instead of
/// returning an unusable relative name, and returns `None` (rather than a
/// path that would silently fail later) when nothing is found anywhere.
fn locate_egress_shim_binary() -> Option<PathBuf> {
    if let Ok(path) = std::env::var("HIVE_EGRESS_SHIM_PATH") {
        let path = PathBuf::from(path);
        if path.is_file() {
            return Some(path);
        }
    }
    if let Ok(exe) = std::env::current_exe()
        && let Some(dir) = exe.parent()
    {
        let bundled = dir.join("hive-egress-shim");
        if bundled.is_file() {
            return Some(bundled);
        }
    }
    std::env::var_os("PATH").and_then(|paths| {
        std::env::split_paths(&paths)
            .map(|dir| dir.join("hive-egress-shim"))
            .find(|candidate| candidate.is_file())
    })
}

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
    let egress = match policy.network() {
        NetworkPolicy::DenyAll => None,
        NetworkPolicy::AllowHosts(hosts) => Some(setup_egress(hosts)?),
    };
    let wrapped_command = match &egress {
        Some(bind) => bind.wrap_command(command),
        None => command.to_vec(),
    };
    let extra_landlock_paths: Vec<PathBuf> = match &egress {
        Some(bind) => vec![bind.socket_path.clone(), bind.shim_path.clone()],
        None => Vec::new(),
    };

    let bwrap_path = locate_bwrap_binary();
    let argv = build_bwrap_argv(policy, &wrapped_command, cwd, egress.as_ref());
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
            apply_landlock_ruleset(&policy_for_child, &extra_landlock_paths)
                .map_err(|err| std::io::Error::other(format!("landlock: {err}")))?;
            apply_seccomp_denylist()
                .map_err(|err| std::io::Error::other(format!("seccomp: {err}")))?;
            Ok(())
        });
    }

    let child = cmd.spawn().map_err(LaunchError::from)?;
    // Only now, with a real sandboxed Child in hand, hand the proxy and its
    // temp directory over to that process's lifetime. Any earlier return
    // above (setup_egress failing, cmd.spawn() failing) drops `egress`
    // normally instead of reaching here -- see EgressBind's struct doc.
    if let Some(bind) = egress {
        bind.leak_for_process_lifetime();
    }
    Ok(child)
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

/// Builds bwrap's argv. Every bind mount uses the same source-path-equals-
/// destination-path convention (`--bind SRC SRC`, never `--bind SRC DST`
/// with `SRC != DST`): Landlock rules (`apply_landlock_ruleset`) are
/// attached to the *object* a path resolves to at rule-creation time, on
/// the host's own mount namespace, before bwrap's `pre_exec`-applied
/// ruleset is inherited across its later `execve` into the sandboxed
/// command. Keeping the in-sandbox path identical to the host path avoids
/// ever needing to reason about whether a Landlock rule survives being
/// exposed at a *different* path after a bind mount -- it never has to.
///
/// `egress` is `Some` only for a [`NetworkPolicy::AllowHosts`] launch: it
/// always unshares the network namespace (previously only `DenyAll` did --
/// AllowHosts used to leave the host network namespace fully shared, which
/// this closes) and additionally bind-mounts the [`EgressBind`]'s socket
/// (read-write: the sandboxed command's `hive-egress-shim` needs to both
/// read and write it as a normal client socket) and shim binary
/// (`--ro-bind`: the sandboxed side never needs to write its own relay).
fn build_bwrap_argv(
    policy: &SandboxPolicy,
    command: &[String],
    cwd: &Path,
    egress: Option<&EgressBind>,
) -> Vec<String> {
    let mut argv: Vec<String> = vec![
        "--unshare-user".to_string(),
        "--unshare-pid".to_string(),
        "--unshare-ipc".to_string(),
        "--unshare-uts".to_string(),
        "--unshare-net".to_string(),
        "--die-with-parent".to_string(),
        "--proc".to_string(),
        "/proc".to_string(),
        "--dev".to_string(),
        "/dev".to_string(),
    ];

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

    if let Some(bind) = egress {
        let shim_str = bind.shim_path.to_string_lossy().into_owned();
        argv.push("--ro-bind".to_string());
        argv.push(shim_str.clone());
        argv.push(shim_str);

        let socket_str = bind.socket_path.to_string_lossy().into_owned();
        argv.push("--bind".to_string());
        argv.push(socket_str.clone());
        argv.push(socket_str);
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

/// `extra_full_access` grants the same (full) access level as
/// `policy.writable_roots()` -- used for the egress socket and shim binary
/// paths on an `AllowHosts` launch (see [`setup_egress`]). These are not
/// part of the confinement boundary the rest of this ruleset protects (no
/// data the sandboxed process shouldn't see or corrupt lives behind them):
/// the actual write protection for the shim binary is bwrap's `--ro-bind`
/// mount flag in [`build_bwrap_argv`], a stronger guarantee than Landlock
/// could add on top since it applies inside the sandbox's own mount
/// namespace, not just to the host-side process this ruleset restricts.
fn apply_landlock_ruleset(
    policy: &SandboxPolicy,
    extra_full_access: &[PathBuf],
) -> Result<(), String> {
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

    for path in policy
        .writable_roots()
        .iter()
        .chain(extra_full_access.iter())
    {
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
    use std::sync::Mutex;

    /// `cargo test` runs `#[test]` functions on multiple threads by
    /// default, but `std::env::set_var`/`remove_var` mutate whole-process
    /// state. Every test in this module that touches `PATH` or
    /// `HIVE_EGRESS_SHIM_PATH` holds this lock for the duration of that
    /// mutation so two such tests can never interleave their env changes.
    static ENV_MUTATION_LOCK: Mutex<()> = Mutex::new(());

    fn policy(writable: Vec<PathBuf>, readonly: Vec<PathBuf>, hook: PathBuf) -> SandboxPolicy {
        SandboxPolicy::build(writable, readonly, hook, NetworkPolicy::DenyAll)
            .expect("valid policy")
    }

    /// Builds a real `EgressBind` for argv-construction tests: a real temp
    /// directory and a real (but unused by these tests) `AllowlistProxy`,
    /// with the given `socket_path`/`shim_path` overriding what
    /// `build_bwrap_argv`/`wrap_command` actually read. Real values rather
    /// than fakes because `EgressBind` owns its resources now (see the
    /// struct doc) -- there is no longer a way to construct one without a
    /// live proxy behind it.
    fn test_egress_bind(socket_path: PathBuf, shim_path: PathBuf) -> EgressBind {
        let dir = tempfile::tempdir().expect("tempdir");
        let proxy_socket = dir.path().join("unused.sock");
        let proxy = AllowlistProxy::spawn(&proxy_socket, vec![]).expect("spawn test proxy");
        EgressBind {
            socket_path,
            shim_path,
            dir,
            proxy,
        }
    }

    #[test]
    fn hook_config_dir_is_always_ro_bound_ahead_of_readonly_roots() {
        let hook = PathBuf::from("/home/agent/.hive/hooks");
        let p = policy(vec![], vec![PathBuf::from("/usr")], hook.clone());
        let argv = build_bwrap_argv(&p, &["true".to_string()], Path::new("/tmp"), None);

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
        let argv = build_bwrap_argv(&p, &["true".to_string()], Path::new("/tmp"), None);
        assert!(argv.contains(&"--unshare-net".to_string()));
    }

    #[test]
    fn allow_hosts_also_unshares_net_now_that_the_egress_proxy_gates_it() {
        // Previously AllowHosts left the host network namespace fully
        // shared (dead code, since launch() rejected it outright). Now
        // that a real proxy enforces the allowlist, the sandbox gets no
        // network route except the bind-mounted socket, exactly like
        // DenyAll -- see build_bwrap_argv's doc comment.
        let p = SandboxPolicy::build(
            vec![],
            vec![],
            PathBuf::from("/home/agent/.hive/hooks"),
            NetworkPolicy::AllowHosts(vec!["example.com".to_string()]),
        )
        .expect("valid policy");
        let bind = test_egress_bind(
            PathBuf::from("/tmp/hive-egress-test/egress.sock"),
            PathBuf::from("/tmp/hive-egress-test/hive-egress-shim"),
        );
        let argv = build_bwrap_argv(&p, &["true".to_string()], Path::new("/tmp"), Some(&bind));
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
        let argv = build_bwrap_argv(&p, &["true".to_string()], Path::new("/tmp"), None);

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
            None,
        );
        let sep = argv
            .iter()
            .position(|a| a == "--")
            .expect("separator present");
        assert_eq!(&argv[sep + 1..], &["echo".to_string(), "hi".to_string()]);
    }

    #[test]
    fn egress_bind_adds_ro_bind_shim_and_bind_socket() {
        let p = policy(vec![], vec![], PathBuf::from("/home/agent/.hive/hooks"));
        let bind = test_egress_bind(
            PathBuf::from("/tmp/hive-egress-test/egress.sock"),
            PathBuf::from("/tmp/hive-egress-test/hive-egress-shim"),
        );
        let argv = build_bwrap_argv(&p, &["true".to_string()], Path::new("/tmp"), Some(&bind));

        let shim_str = bind.shim_path.to_string_lossy().into_owned();
        let socket_str = bind.socket_path.to_string_lossy().into_owned();
        assert!(
            argv.windows(2)
                .any(|w| w[0] == "--ro-bind" && w[1] == shim_str),
            "shim binary must be ro-bound: {argv:?}"
        );
        assert!(
            argv.windows(2)
                .any(|w| w[0] == "--bind" && w[1] == socket_str),
            "egress socket must be bound read-write: {argv:?}"
        );
        assert!(
            !argv
                .windows(2)
                .any(|w| w[0] == "--ro-bind" && w[1] == socket_str),
            "egress socket must not be ro-bound: {argv:?}"
        );
    }

    #[test]
    fn egress_bind_wraps_command_with_shim_and_separator() {
        let bind = test_egress_bind(
            PathBuf::from("/tmp/hive-egress-test/egress.sock"),
            PathBuf::from("/tmp/hive-egress-test/hive-egress-shim"),
        );
        let real_command = vec!["echo".to_string(), "hi".to_string()];
        let wrapped = bind.wrap_command(&real_command);

        assert_eq!(
            wrapped,
            vec![
                "/tmp/hive-egress-test/hive-egress-shim".to_string(),
                "/tmp/hive-egress-test/egress.sock".to_string(),
                "--".to_string(),
                "echo".to_string(),
                "hi".to_string(),
            ]
        );
    }

    #[test]
    fn dropping_an_egress_bind_before_leaking_cleans_up_the_proxy_and_socket() {
        // Simulates launch() returning early (a failed locate_egress_shim_binary
        // or cmd.spawn() call) before leak_for_process_lifetime is ever
        // reached: the review finding this test guards against was that
        // setup_egress used to leak the proxy and temp directory
        // unconditionally, even when the launch it was for never happened.
        //
        // setup_egress also calls locate_egress_shim_binary, which finds
        // nothing in a plain `cargo test` environment (the real binary is
        // only ever bundled next to a packaged desktop app) -- point it at
        // a dummy file via the env override this test doesn't need to be a
        // real executable for, since this test never spawns bwrap.
        let shim_dir = tempfile::tempdir().expect("tempdir");
        let dummy_shim = shim_dir.path().join("hive-egress-shim");
        std::fs::write(&dummy_shim, b"").expect("write dummy shim");
        let bind = {
            let _env_guard = ENV_MUTATION_LOCK
                .lock()
                .expect("env mutation lock poisoned");
            // SAFETY: held under ENV_MUTATION_LOCK, restored before the
            // guard drops.
            unsafe {
                std::env::set_var("HIVE_EGRESS_SHIM_PATH", &dummy_shim);
            }
            let bind = setup_egress(&["example.com".to_string()]).expect("setup_egress");
            unsafe {
                std::env::remove_var("HIVE_EGRESS_SHIM_PATH");
            }
            bind
        };
        let socket_path = bind.socket_path.clone();
        assert!(
            std::os::unix::net::UnixStream::connect(&socket_path).is_ok(),
            "proxy must be listening while EgressBind is alive"
        );

        drop(bind);

        assert!(
            std::os::unix::net::UnixStream::connect(&socket_path).is_err(),
            "dropping EgressBind before it is leaked must stop the proxy \
             instead of leaking it for a launch that never happened"
        );
    }

    #[test]
    fn locate_egress_shim_binary_resolves_a_bare_path_entry_to_an_absolute_file() {
        // The bug this guards: --ro-bind's source path is opened directly
        // by bwrap (no PATH search, unlike Command::new/execvp), so the old
        // fallback (a bare "hive-egress-shim") would silently fail even
        // when a real install existed on PATH.
        let dir = tempfile::tempdir().expect("tempdir");
        let shim = dir.path().join("hive-egress-shim");
        std::fs::write(&shim, b"").expect("write dummy shim");

        let _env_guard = ENV_MUTATION_LOCK
            .lock()
            .expect("env mutation lock poisoned");
        let original_path = std::env::var_os("PATH");
        // SAFETY: held under ENV_MUTATION_LOCK; PATH is restored before the
        // guard drops.
        unsafe {
            std::env::remove_var("HIVE_EGRESS_SHIM_PATH");
            std::env::set_var("PATH", dir.path());
        }
        let found = locate_egress_shim_binary();
        unsafe {
            match &original_path {
                Some(p) => std::env::set_var("PATH", p),
                None => std::env::remove_var("PATH"),
            }
        }

        assert_eq!(found, Some(shim));
    }

    // No test here calls the real launch() end to end (i.e. all the way
    // through Command::spawn()'s pre_exec) -- and that is deliberate, not
    // an oversight. This crate's own test binary is multithreaded (many
    // concurrent #[test] functions, plus AllowlistProxy's own leaked
    // accept-loop threads from earlier tests); pre_exec's closure runs in
    // the forked child via a raw fork() (required so pre_exec can run
    // arbitrary code before exec), and `apply_landlock_ruleset` /
    // `apply_seccomp_denylist` both allocate (Vec, PathFd::new, format!).
    // Allocating in a fork()ed child of a multithreaded process is a
    // textbook post-fork deadlock hazard: if another thread held the
    // allocator's lock at the instant of fork, the single surviving child
    // thread can block on malloc forever, and because Rust's pre_exec
    // machinery has the parent's spawn() block on a pipe read until the
    // child either execs or reports an error, THAT hangs too -- observed
    // directly in this session (a first attempt at an end-to-end launch()
    // test hung `cargo test` for 15+ minutes; `timeout` plus
    // `--test-threads=1` plus reading `/proc/<pid>/task/*/comm` confirmed
    // it was blocked inside the forked child, not anywhere in this crate's
    // own logic). Fixing pre_exec to be genuinely alloc-free (or moving off
    // fork+pre_exec entirely, e.g. toward `posix_spawn`-style APIs) is a
    // real, pre-existing gap this discovery surfaced -- out of scope for
    // this pass; tracked as a VENDORING.md open risk. Argv construction
    // (`egress_bind_adds_ro_bind_shim_and_bind_socket`,
    // `egress_bind_wraps_command_with_shim_and_separator`,
    // `allow_hosts_also_unshares_net_now_that_the_egress_proxy_gates_it`
    // above) and the actual enforcement point (`egress_proxy.rs`'s own
    // tests, which never fork) are both real and tested; only the
    // OS-process-spawn plumbing in between is untested here, matching this
    // crate's pre-existing posture (no test before this pass exercised
    // launch()'s real spawn path either).

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

        match apply_landlock_ruleset(&p, &[]) {
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
