//! Test-only harness (NOT part of the desktop app's shipped binaries):
//! starts the real, shipped [`hive_desktop_sandbox::egress_proxy::AllowlistProxy`]
//! on a Unix socket and parks until killed. Driven by
//! `apps/desktop-sandbox/scripts/egress-bwrap-e2e.sh`, which bind-mounts that
//! socket into a real bubblewrap sandbox and proves that a disallowed
//! destination is refused at the allowlist boundary while an allowed one
//! succeeds.
//!
//! It deliberately reuses the production `AllowlistProxy` rather than a
//! stand-in proxy: the point of the end-to-end test is to exercise the exact
//! enforcement code that ships, not a look-alike.
//!
//! Usage: egress-proxy-harness <socket_path> [allowed_host...]
//!
//! Linux-only (the egress proxy is `#[cfg(target_os = "linux")]`); a no-op
//! stub keeps the `--all-targets` Windows cross-check compiling, matching how
//! `src/bin/hive-egress-shim.rs` is gated.

#[cfg(target_os = "linux")]
fn main() {
    use hive_desktop_sandbox::egress_proxy::AllowlistProxy;
    use std::io::Write as _;
    use std::path::PathBuf;

    let mut args = std::env::args().skip(1);
    let Some(socket_path) = args.next() else {
        eprintln!(
            "egress-proxy-harness: usage: egress-proxy-harness <socket_path> [allowed_host...]"
        );
        std::process::exit(2);
    };
    let allowed_hosts: Vec<String> = args.collect();

    let proxy = match AllowlistProxy::spawn(&PathBuf::from(&socket_path), allowed_hosts.clone()) {
        Ok(proxy) => proxy,
        Err(err) => {
            eprintln!("egress-proxy-harness: failed to bind {socket_path}: {err}");
            std::process::exit(1);
        }
    };

    // The proxy is listening the moment `spawn` returns; announce readiness so
    // the driving script can stop polling. Keep it running for the rest of
    // this process's life (the script kills the process on teardown) using
    // the same API `linux::launch` uses for a real sandboxed task.
    println!("READY {socket_path} allow={allowed_hosts:?}");
    let _ = std::io::stdout().flush();
    proxy.leak_for_process_lifetime();

    loop {
        std::thread::sleep(std::time::Duration::from_secs(3600));
    }
}

#[cfg(not(target_os = "linux"))]
fn main() {
    eprintln!("egress-proxy-harness: Linux-only, not supported on this platform");
    std::process::exit(1);
}
