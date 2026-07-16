//! Thin binary run *inside* the sandbox for a
//! [`hive_desktop_sandbox::NetworkPolicy::AllowHosts`] launch. Contract:
//!
//!   hive-egress-shim <unix_socket_path> -- <real command> [args...]
//!
//! `linux::launch` builds this argv (never a shell string -- no
//! interpolation, no injection surface) and bind-mounts both this binary
//! and the socket path verbatim (source path == destination path, matching
//! every other bind mount `build_bwrap_argv` constructs). On start: bind an
//! ephemeral loopback TCP port relaying to the socket
//! (`hive_desktop_sandbox::shim::start_loopback_bridge`), export
//! `HTTP_PROXY`/`HTTPS_PROXY`/`NO_PROXY` pointing at it, then `execvp` into
//! the real command, replacing this process so PID/signal handling stay
//! exactly as if the real command had been launched directly.
//!
//! This binary only ever runs on Linux (it is only ever bind-mounted and
//! exec'd by `linux::launch`). `main` is split into a real,
//! `#[cfg(target_os = "linux")]`-gated implementation and a no-op stub so
//! this bin target still compiles when the crate is cross-checked against
//! Windows (VENDORING.md's `cargo clippy --all-targets --target
//! x86_64-pc-windows-gnu` verification step), matching how
//! `egress_proxy.rs` and `shim.rs` are gated in `lib.rs`.

#[cfg(target_os = "linux")]
fn main() {
    use std::os::unix::process::CommandExt;
    use std::path::PathBuf;
    use std::process::Command;

    let args: Vec<String> = std::env::args().collect();
    let Some(sep) = args.iter().position(|a| a == "--") else {
        eprintln!(
            "hive-egress-shim: usage: hive-egress-shim <unix_socket_path> -- <command> [args...]"
        );
        std::process::exit(2);
    };
    if sep < 2 || sep + 1 >= args.len() {
        eprintln!("hive-egress-shim: missing socket path or command");
        std::process::exit(2);
    }

    let socket_path = PathBuf::from(&args[1]);
    let command = &args[sep + 1..];

    let port = match hive_desktop_sandbox::shim::start_loopback_bridge(&socket_path) {
        Ok(p) => p,
        Err(e) => {
            eprintln!("hive-egress-shim: failed to start loopback bridge: {e}");
            std::process::exit(1);
        }
    };

    for (key, value) in hive_desktop_sandbox::shim::proxy_env_vars(port) {
        // SAFETY: single-threaded at this point (the bridge accept loop
        // runs on its own thread, but nothing else in this process reads
        // or writes the environment concurrently with this call).
        unsafe {
            std::env::set_var(key, value);
        }
    }

    // Never returns on success: replaces this process image entirely, so
    // the real command inherits this process's PID and becomes the direct
    // child bwrap/Landlock/seccomp confined in the first place.
    let err = Command::new(&command[0]).args(&command[1..]).exec();
    eprintln!("hive-egress-shim: exec failed: {err}");
    std::process::exit(1);
}

#[cfg(not(target_os = "linux"))]
fn main() {
    eprintln!("hive-egress-shim: Linux-only, not supported on this platform");
    std::process::exit(1);
}
