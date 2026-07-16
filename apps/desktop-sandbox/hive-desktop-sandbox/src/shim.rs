//! Loopback-TCP <-> Unix-socket relay run *inside* the sandbox by the
//! `hive-egress-shim` binary (`src/bin/hive-egress-shim.rs`), before it
//! `execve`s into the real sandboxed command.
//!
//! Why this exists: `linux::launch` bind-mounts the host-side
//! [`crate::egress_proxy::AllowlistProxy`]'s Unix socket into the sandbox
//! verbatim (bind mounts are unaffected by `--unshare-net`), but most
//! HTTP/HTTPS client libraries only know how to speak to an `HTTP_PROXY`
//! given as a `host:port` URL, not a `unix:` socket path. This module
//! bridges the two: bind an ephemeral loopback TCP port *inside* the
//! sandbox's own isolated network namespace (the only namespace this
//! bridge ever runs in -- the shim only runs once bwrap has already execed
//! it inside the sandbox), and relay each accepted connection to the
//! bind-mounted Unix socket. `HTTP_PROXY`/`HTTPS_PROXY` then point at that
//! loopback port.

use std::io::Result;
use std::net::{TcpListener, TcpStream};
use std::os::unix::net::UnixStream;
use std::path::Path;

/// Binds an ephemeral loopback TCP port and starts relaying every accepted
/// connection to `unix_socket_path`, one thread per connection, until the
/// process exits (the shim never stops this deliberately -- it execs into
/// the real command right after, so the accept loop's lifetime is the
/// sandboxed task's lifetime).
///
/// Returns the bound port so the caller can put it in `HTTP_PROXY`.
pub fn start_loopback_bridge(unix_socket_path: &Path) -> Result<u16> {
    let listener = TcpListener::bind("127.0.0.1:0")?;
    let port = listener.local_addr()?.port();
    let socket_path = unix_socket_path.to_path_buf();

    std::thread::spawn(move || {
        accept_forever(listener, &socket_path);
    });

    Ok(port)
}

fn accept_forever(listener: TcpListener, unix_socket_path: &Path) {
    for conn in listener.incoming() {
        let Ok(tcp) = conn else { continue };
        let socket_path = unix_socket_path.to_path_buf();
        std::thread::spawn(move || {
            let _ = relay_one(tcp, &socket_path);
        });
    }
}

fn relay_one(tcp: TcpStream, unix_socket_path: &Path) -> Result<()> {
    let unix = UnixStream::connect(unix_socket_path)?;

    let mut tcp_read = tcp.try_clone()?;
    let mut unix_write = unix.try_clone()?;
    let mut unix_read = unix;
    let mut tcp_write = tcp;

    let to_unix = std::thread::spawn(move || {
        let _ = std::io::copy(&mut tcp_read, &mut unix_write);
    });
    let _ = std::io::copy(&mut unix_read, &mut tcp_write);
    let _ = to_unix.join();
    Ok(())
}

/// Sets the env vars a proxy-aware HTTP client reads, pointing at the
/// loopback bridge on `port`. Split out from `start_loopback_bridge` so
/// tests can assert the exact var names/values without needing a real
/// bridge running.
pub fn proxy_env_vars(port: u16) -> Vec<(&'static str, String)> {
    let proxy_url = format!("http://127.0.0.1:{port}");
    vec![
        ("HTTP_PROXY", proxy_url.clone()),
        ("HTTPS_PROXY", proxy_url),
        ("NO_PROXY", "127.0.0.1,localhost".to_string()),
    ]
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::io::{Read, Write};
    use std::os::unix::net::UnixListener;
    use std::path::PathBuf;

    fn temp_socket_path(label: &str) -> PathBuf {
        std::env::temp_dir().join(format!(
            "hive-egress-shim-test-{label}-{}-{}.sock",
            std::process::id(),
            label.len()
        ))
    }

    #[test]
    fn relays_bytes_from_loopback_tcp_to_unix_socket_and_back() {
        let socket_path = temp_socket_path("relay");
        let _ = std::fs::remove_file(&socket_path);
        let unix_listener = UnixListener::bind(&socket_path).unwrap();

        // Fake "proxy": accepts one Unix connection, echoes whatever it
        // reads back -- enough to prove the bridge actually carries bytes
        // in both directions, not just that it connects.
        std::thread::spawn(move || {
            if let Ok((mut stream, _)) = unix_listener.accept() {
                let mut buf = [0u8; 64];
                if let Ok(n) = stream.read(&mut buf) {
                    let _ = stream.write_all(&buf[..n]);
                }
            }
        });

        let port = start_loopback_bridge(&socket_path).unwrap();

        let mut tcp = TcpStream::connect(("127.0.0.1", port)).unwrap();
        tcp.write_all(b"ping").unwrap();
        let mut buf = [0u8; 4];
        tcp.read_exact(&mut buf).unwrap();
        assert_eq!(&buf, b"ping");

        std::fs::remove_file(&socket_path).ok();
    }

    #[test]
    fn start_loopback_bridge_returns_a_nonzero_port() {
        let socket_path = temp_socket_path("port-nonzero");
        let _ = std::fs::remove_file(&socket_path);
        // No listener needed for this assertion: only the TCP side of the
        // bridge is under test here.
        let _unix_listener = UnixListener::bind(&socket_path).unwrap();

        let port = start_loopback_bridge(&socket_path).unwrap();
        assert_ne!(port, 0);

        std::fs::remove_file(&socket_path).ok();
    }

    #[test]
    fn proxy_env_vars_point_at_the_given_port() {
        let vars = proxy_env_vars(3129);
        assert_eq!(
            vars,
            vec![
                ("HTTP_PROXY", "http://127.0.0.1:3129".to_string()),
                ("HTTPS_PROXY", "http://127.0.0.1:3129".to_string()),
                ("NO_PROXY", "127.0.0.1,localhost".to_string()),
            ]
        );
    }
}
