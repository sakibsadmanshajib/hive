//! Hive command-runner binary (Step 3 Integration A).
//!
//! This is the per-task runner that `runner_client::spawn_runner_transport`
//! starts AS the low-privilege sandbox account via `CreateProcessWithLogonW`
//! (blueprint A.Q1). It speaks the framed named-pipe IPC, derives a
//! capability-restricted primary token from its OWN token, applies the per-task
//! ACL confinement, and spawns the inner child under that token with
//! `spawn_process_with_pipes` (the `tty = false` path). A `tty = true` request
//! is refused fail-closed (ConPTY is Step 5); see
//! [`hive_desktop_sandbox::windows_elevated::run_command_runner`].
//!
//! Port of the `tty = false` branch of upstream
//! `bin/command_runner/win.rs`; ConPTY (`tty = true`, `spawn_conpty_process_as_user`,
//! `ResizePseudoConsole`) is stubbed out, not vendored.
//!
//! Manifest: this binary relies on the OS default `asInvoker` execution level
//! for an unmanifested exe; it must NOT auto-elevate, since it runs as the
//! unprivileged sandbox account. Its name contains no installer-detection
//! trigger word, so the OS does not auto-elevate it.
//!
//! Verification: the Win32 behaviour is lab-gated on `spike307-win` (D-004); CI
//! only cross-compiles it for `x86_64-pc-windows-gnu`.

#[cfg(windows)]
fn main() -> std::process::ExitCode {
    let args: Vec<String> = std::env::args().collect();
    // Earliest observable point: proves the runner image loaded and main ran,
    // and whether the pipe arguments arrived (diagnostic, gated).
    hive_desktop_sandbox::windows_elevated::runner_debug_log(&format!(
        "hive-command-runner main entry: argc={} has_pipe_in={} has_pipe_out={}",
        args.len(),
        args.iter().any(|a| a.starts_with("--pipe-in=")),
        args.iter().any(|a| a.starts_with("--pipe-out=")),
    ));
    let pipe_in = arg_value(&args, "--pipe-in");
    let pipe_out = arg_value(&args, "--pipe-out");
    let (Some(pipe_in), Some(pipe_out)) = (pipe_in, pipe_out) else {
        hive_desktop_sandbox::windows_elevated::runner_debug_log(
            "exiting 2: missing --pipe-in/--pipe-out",
        );
        eprintln!("usage: hive-command-runner --pipe-in=<name> --pipe-out=<name>");
        return std::process::ExitCode::from(2);
    };
    match hive_desktop_sandbox::windows_elevated::run_command_runner(&pipe_in, &pipe_out) {
        Ok(()) => std::process::ExitCode::SUCCESS,
        Err(e) => {
            eprintln!("hive-command-runner failed: {e}");
            std::process::ExitCode::FAILURE
        }
    }
}

#[cfg(windows)]
fn arg_value(args: &[String], key: &str) -> Option<String> {
    let prefix = format!("{key}=");
    args.iter()
        .find_map(|a| a.strip_prefix(&prefix).map(str::to_string))
}

/// Non-Windows: the runner is meaningless off Windows. Fail closed rather than
/// pretend, matching the crate's honest-stub discipline (D-005).
#[cfg(not(windows))]
fn main() -> std::process::ExitCode {
    eprintln!("hive-command-runner is only functional on Windows");
    std::process::ExitCode::FAILURE
}
