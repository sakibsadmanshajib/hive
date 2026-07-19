//! Desktop native sandbox backends for the Hive agent subsystem
//! (blueprint Step 4.2, issue #306).
//!
//! This crate is the single stable interface the desktop app (and, later,
//! the OpenHands `workspace_factory` plugin point) calls to run an agent
//! task under a platform-native OS sandbox. Platform mechanics (Landlock,
//! seccomp-BPF, bubblewrap on Linux; restricted tokens, ACLs, Job Objects,
//! Windows Firewall on Windows) live behind [`launch`] and are not part of
//! the public API. macOS (Seatbelt) is out of scope; see blueprint Step 5.6.
//!
//! See `VENDORING.md` for what was vendored from `openai/codex`
//! (Apache-2.0) versus newly authored here, and for open risks.

pub mod policy;
pub mod windows_plan;

#[cfg(target_os = "linux")]
pub mod egress_proxy;
#[cfg(target_os = "linux")]
mod linux;
#[cfg(target_os = "linux")]
pub mod shim;
#[cfg(windows)]
mod windows;

#[cfg(not(any(target_os = "linux", windows)))]
compile_error!(
    "hive-desktop-sandbox supports only Linux and Windows for the demo desktop backends; \
     macOS Seatbelt is deferred to blueprint Step 5.6"
);

pub use policy::{NetworkPolicy, PolicyError, SandboxPolicy};
#[cfg(windows)]
pub use windows::SandboxChild;

use std::path::Path;

/// Error launching a command inside the sandbox described by a
/// [`SandboxPolicy`].
#[derive(Debug)]
pub enum LaunchError {
    /// Windows-only now (blueprint Step 4.4, #308/#311, closed the Linux
    /// side): the policy requested [`NetworkPolicy::AllowHosts`], which
    /// `windows::launch` does not yet enforce (WFP egress is a later step). It
    /// applies the restricted token, directory ACL, and Job Object for
    /// `DenyAll`, but returns this error for `AllowHosts` rather than launch
    /// with an unenforced network policy. `linux::launch` no longer returns
    /// this: a real allowlist-enforcing proxy (`egress_proxy.rs`) backs
    /// `AllowHosts` there. Reject rather than silently launching with full
    /// network access or full denial.
    AllowHostsNotYetImplemented,
    /// Spawning the sandboxed process failed.
    Io(std::io::Error),
    /// Linux-only: applying the Landlock ruleset or seccomp-BPF filter
    /// failed.
    #[cfg(target_os = "linux")]
    Confinement(String),
    /// Windows-only: creating the restricted token, ACL, Job Object, or
    /// firewall rule failed.
    #[cfg(windows)]
    Confinement(String),
}

impl std::fmt::Display for LaunchError {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        match self {
            LaunchError::AllowHostsNotYetImplemented => {
                write!(
                    f,
                    "NetworkPolicy::AllowHosts is not yet enforced by this crate"
                )
            }
            LaunchError::Io(err) => write!(f, "failed to spawn sandboxed process: {err}"),
            #[cfg(any(target_os = "linux", windows))]
            LaunchError::Confinement(msg) => {
                write!(f, "failed to confine sandboxed process: {msg}")
            }
        }
    }
}

impl std::error::Error for LaunchError {}

impl From<std::io::Error> for LaunchError {
    fn from(err: std::io::Error) -> Self {
        LaunchError::Io(err)
    }
}

/// Launches `command` (argv, `command[0]` is the program) with working
/// directory `cwd`, confined by `policy`, using the platform-native backend.
///
/// The returned handle type is platform-specific because the two backends own
/// fundamentally different OS resources: Linux returns a
/// [`std::process::Child`] for the confined bwrap process, while Windows
/// returns a `SandboxChild` that also owns the Job Object handle whose closure
/// kills the process tree.
#[cfg(target_os = "linux")]
pub fn launch(
    policy: &SandboxPolicy,
    command: &[String],
    cwd: &Path,
) -> Result<std::process::Child, LaunchError> {
    linux::launch(policy, command, cwd)
}

/// See the Linux [`launch`] for the shared contract. Windows returns a
/// [`SandboxChild`], which owns the child process handle and the sole
/// kill-on-close Job Object handle (dropping it terminates the process tree).
#[cfg(windows)]
pub fn launch(
    policy: &SandboxPolicy,
    command: &[String],
    cwd: &Path,
) -> Result<windows::SandboxChild, LaunchError> {
    windows::launch(policy, command, cwd)
}
