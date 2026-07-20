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
pub mod windows_elevated;
pub mod windows_plan;
pub mod windows_resolve;
// Cross-platform (no Win32) pieces of the Windows WFP egress fence
// (Integration B2): the loopback proxy port range and the loopback-allowlist
// port complement math, unit-tested on this crate's Linux CI job.
pub mod wfp_ports;

// Egress proxy: the Linux transport is a Unix socket; the Windows transport is
// a loopback TCP listener (Integration B2 activation). Compiled on both, with
// the shared allowlist/DNS logic single-sourced (see `egress_proxy.rs`).
#[cfg(any(target_os = "linux", windows))]
pub mod egress_proxy;
#[cfg(target_os = "linux")]
mod linux;
#[cfg(target_os = "linux")]
pub mod shim;
#[cfg(windows)]
mod windows;
// Windows Firewall COM half of the two-layer egress fence (Integration B2,
// CTO Q1). LIVE (Integration B2 activation): `launch` dispatches to the
// SID-fenced elevated compose, which wires `ensure_offline_outbound_block`
// (provision) and `ensure_offline_proxy_allowlist` / `teardown_offline_proxy_allowlist`
// (per task) into the launch path, so the earlier `allow(dead_code)` is gone.
#[cfg(windows)]
mod windows_firewall;

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
    /// LEGACY (Integration B2 activation): no longer produced by `launch`.
    /// Before B2, `windows::launch` returned this to refuse
    /// [`NetworkPolicy::AllowHosts`] rather than launch under an unenforced
    /// egress control. B2 wired the SID-fenced compose (WFP + firewall +
    /// loopback proxy), so `AllowHosts` now launches enforced. Retained as
    /// part of the public error API; construction sites are gone.
    AllowHostsNotYetImplemented,
    /// LEGACY (Integration B2 activation): no longer produced by `launch`.
    /// Before B2, `windows::launch` returned this to refuse
    /// [`NetworkPolicy::DenyAll`] rather than launch a process while claiming a
    /// block-all egress control that was not in force. B2 installs the
    /// persistent WFP + firewall block-all at provision and enforces it per
    /// task, so `DenyAll` now launches enforced. Retained as part of the
    /// public error API; construction sites are gone.
    NetworkConfinementNotImplemented,
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
                    "legacy LaunchError::AllowHostsNotYetImplemented (AllowHosts is now enforced; this variant is no longer produced)"
                )
            }
            LaunchError::NetworkConfinementNotImplemented => {
                write!(
                    f,
                    "legacy LaunchError::NetworkConfinementNotImplemented (DenyAll is now enforced; this variant is no longer produced)"
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

// The former `network_policies_still_report_as_not_yet_enforced` inert-boundary
// guard was removed with Integration B2 activation: `launch` no longer refuses
// either network policy. The live NetworkPolicy -> LaunchDecision::Spawn*
// mapping is asserted in `windows_elevated`'s tests instead.
