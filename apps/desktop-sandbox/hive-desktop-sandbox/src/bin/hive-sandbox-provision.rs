//! Hive sandbox setup / provisioning binary (Step 3 Integration A).
//!
//! One-time, ELEVATED provisioning of the single shared low-privilege sandbox
//! account (Q3). It creates the least-privilege local group and account, grants
//! the interactive-logon right and ensures the Secondary Logon service is
//! enabled (so `CreateProcessWithLogonW` can use the hidden account, blueprint
//! A.Q1), DPAPI-seals a CSPRNG password into a secrets directory whose ACL
//! excludes the sandbox account, hides the account and its profile directory,
//! and writes the readiness marker. It installs NO network egress control (WFP
//! and the COM firewall block are Integration B; the upstream setup binary's
//! `install_wfp_filters` / `ensure_offline_*` call sites are OMITTED here).
//!
//! See [`hive_desktop_sandbox::windows_elevated::provision_sandbox_account`].
//!
//! ## Elevation
//!
//! This binary relies on the OS default `asInvoker` execution level for an
//! unmanifested exe, matching upstream's `bin/setup_main` intent (`asInvoker`,
//! NOT `requireAdministrator`). It is deliberately named `hive-sandbox-provision`
//! rather than `*-setup`/`*-install`: a bare exe whose filename matches Windows
//! installer-detection heuristics (`setup`, `install`, `update`, `patch`) is
//! auto-elevated by the OS even without a manifest, which would be an
//! unintended, silent UAC prompt. Avoiding those trigger words in the name
//! gives the correct `asInvoker` behaviour with no embedded manifest or build
//! script. It is the ELEVATED WORKER: the desktop app's launch
//! path runs it once via `ShellExecute "runas"` (the single UAC consent, lab
//! item L1) when `sandbox_setup_is_complete` is false. That app-side elevation
//! trigger lives in the desktop shell (Tauri), not in this crate, which is
//! deliberately the OS-mechanism layer. Run directly, it must already be
//! elevated; a non-elevated run fails at the first privileged step (fail-closed,
//! never a partial account).
//!
//! ## Configuration
//!
//! The sandbox home directory is read from the `HIVE_SANDBOX_HOME` environment
//! variable (required); there is no implicit default, so provisioning never
//! writes an account marker to an unexpected location.
//!
//! Verification: the Win32 behaviour is lab-gated on `spike307-win` (D-004); CI
//! only cross-compiles it for `x86_64-pc-windows-gnu`.

#[cfg(windows)]
fn main() -> std::process::ExitCode {
    let sandbox_home = match std::env::var_os("HIVE_SANDBOX_HOME") {
        Some(v) => std::path::PathBuf::from(v),
        None => {
            eprintln!("HIVE_SANDBOX_HOME must be set to the Hive sandbox home directory");
            return std::process::ExitCode::from(2);
        }
    };
    let mut log = std::io::stderr();
    match hive_desktop_sandbox::windows_elevated::provision_sandbox_account(&sandbox_home, &mut log)
    {
        Ok(()) => std::process::ExitCode::SUCCESS,
        Err(e) => {
            eprintln!("hive-sandbox-setup provisioning failed: {e}");
            std::process::ExitCode::FAILURE
        }
    }
}

/// Non-Windows: provisioning is Windows-only. Fail closed (D-005).
#[cfg(not(windows))]
fn main() -> std::process::ExitCode {
    eprintln!("hive-sandbox-setup is only functional on Windows");
    std::process::ExitCode::FAILURE
}
