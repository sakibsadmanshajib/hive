//! Allowlist-enforcing CONNECT proxy for [`crate::policy::NetworkPolicy::AllowHosts`]
//! (blueprint Step 4.4, issue #308/#311). Listens on a Unix domain socket
//! (not a TCP port) so it can be bind-mounted verbatim into a bwrap sandbox
//! that has its own network namespace (`--unshare-net`): a bind-mounted
//! socket file is a filesystem object, unaffected by network-namespace
//! isolation, so it is the *only* way traffic can leave a sandbox whose
//! netns has no other interface than `lo`. This mirrors
//! `apps/agent-engine/internal/egressproxy` (the server-side Go
//! implementation for the exact same #308 SSOT shape) closely enough that a
//! reviewer familiar with one recognizes the other: allowlist check first
//! (deny before any DNS lookup happens), then a single DNS resolution whose
//! result is pinned and dialed literally (never re-resolved), closing the
//! same short-TTL DNS-rebind window the Go version's doc comment calls out,
//! and — issue #342 item 2 — rejecting any resolved address that is private,
//! loopback, or link-local so an allowed hostname rebound to an internal IP
//! (`127.0.0.1`, `169.254.x.x`, RFC1918/ULA) cannot be used to reach
//! host-local or LAN services from inside the sandbox. The Go
//! `resolvePinnedAddr` carries the identical guard so the two surfaces stay
//! the same shape.
//!
//! CONNECT-only (no plain-HTTP forward proxying): virtually all traffic this
//! proxy exists to gate is HTTPS (LLM provider APIs, package registries),
//! and CONNECT tunnelling covers that fully. A plain-HTTP forward path can
//! be added the same way `egressproxy.Proxy.handleForward` does if a real
//! caller ever needs it.

use std::collections::HashSet;
use std::io::{BufRead, BufReader, Read, Write};
use std::net::{IpAddr, Ipv4Addr, Ipv6Addr, TcpStream, ToSocketAddrs};
use std::os::unix::net::{UnixListener, UnixStream};
use std::path::{Path, PathBuf};
use std::sync::Arc;
use std::sync::atomic::{AtomicBool, AtomicUsize, Ordering};
use std::thread::JoinHandle;
use std::time::Duration;

/// How often the accept loop wakes up to check the shutdown flag when no
/// connection has arrived. Small enough that `shutdown()` returns promptly
/// in tests, large enough not to busy-loop.
const ACCEPT_POLL_INTERVAL: Duration = Duration::from_millis(20);

/// Maximum bytes read while looking for the end of a single request or
/// header line, before the first `\r\n`. Bounds an otherwise unbounded
/// `read_line` against a hostile client that dribbles a line without ever
/// terminating it (issue #342 item 3). 8 KiB is far more than a real
/// `CONNECT host:port HTTP/1.1` line or any normal header needs.
const MAX_REQUEST_LINE_BYTES: u64 = 8 * 1024;

/// Maximum number of header lines consumed after the request line before the
/// terminating blank line, so a client cannot stream unbounded headers to
/// hold a proxy thread open (issue #342 item 3).
const MAX_HEADER_LINES: usize = 100;

/// Read deadline applied only while reading the request line and headers, so
/// a slow or silent client cannot pin a handler thread indefinitely (issue
/// #342 item 3). Cleared before the relay phase begins, since a legitimate
/// tunnel (streaming LLM responses) may idle far longer than this.
const CLIENT_HEADER_READ_TIMEOUT: Duration = Duration::from_secs(30);

/// Ceiling on concurrently handled connections. Each accepted connection
/// spawns a handler thread; without a cap a flood of connections spawns
/// unbounded threads (issue #342 item 3). Over the ceiling, new connections
/// are refused with `503` and closed without spawning a handler.
const MAX_CONCURRENT_CONNECTIONS: usize = 256;

/// A running allowlist-enforcing proxy, listening on a Unix socket.
/// `Drop` shuts it down; call [`AllowlistProxy::leak_for_process_lifetime`]
/// instead of dropping it when the caller wants the proxy to outlive this
/// value's scope (the real `launch()` case: the proxy must stay up for as
/// long as the sandboxed task runs, which is long after `launch()` returns).
pub struct AllowlistProxy {
    socket_path: PathBuf,
    shutdown: Arc<AtomicBool>,
    accept_handle: Option<JoinHandle<()>>,
}

impl AllowlistProxy {
    /// Binds `socket_path` (removing a stale file there first) and starts
    /// accepting connections in a background thread. `allowed_hosts` is the
    /// exact wire shape `apps/control-plane/internal/egress`'s
    /// `effective_response.allowed_hosts` returns -- hostnames/IPs, no
    /// ports, case-sensitivity not guaranteed by the SSOT so this proxy
    /// matches case-insensitively (mirrors `egressproxy.Proxy.isAllowed`).
    pub fn spawn(socket_path: &Path, allowed_hosts: Vec<String>) -> std::io::Result<Self> {
        Self::spawn_with_policy(socket_path, allowed_hosts, false)
    }

    /// Test-only sibling of [`AllowlistProxy::spawn`] that permits resolved
    /// destinations which are private/loopback/link-local. The shipped path
    /// (`spawn`) always forbids them -- a sandboxed task must never reach
    /// host-local or LAN services -- and that guard is exercised directly by
    /// `resolve_pinned_rejects_private_loopback_and_link_local`. The only
    /// caller is the `egress-proxy-harness` example: its e2e
    /// (`scripts/egress-bwrap-e2e.sh`) uses two loopback IPs (127.0.0.2 vs
    /// 127.0.0.3) as a throwaway stand-in "internet", so it must opt out of
    /// the loopback-destination guard for the allowed target to be dialable.
    /// The allowlist host check (`is_allowed`) and the netns isolation the
    /// e2e proves are unaffected -- only the redundant destination-IP guard
    /// is relaxed, so the test still genuinely proves a disallowed host is
    /// refused (403) while an allowed one is dialed (200).
    pub fn spawn_allowing_local_dest(
        socket_path: &Path,
        allowed_hosts: Vec<String>,
    ) -> std::io::Result<Self> {
        Self::spawn_with_policy(socket_path, allowed_hosts, true)
    }

    /// As [`AllowlistProxy::spawn`], with control over whether resolved
    /// destinations that are private/loopback/link-local are permitted.
    /// Production always passes `allow_local_dest = false` (via `spawn`): a
    /// sandboxed task must never reach host-local or LAN addresses. Tests
    /// that dial a `127.0.0.1` echo server pass `true`, since blocking
    /// loopback is exactly the guard being exercised elsewhere.
    fn spawn_with_policy(
        socket_path: &Path,
        allowed_hosts: Vec<String>,
        allow_local_dest: bool,
    ) -> std::io::Result<Self> {
        let _ = std::fs::remove_file(socket_path);
        let listener = UnixListener::bind(socket_path)?;
        listener.set_nonblocking(true)?;

        let allowed: Arc<HashSet<String>> = Arc::new(
            allowed_hosts
                .into_iter()
                .map(|h| h.to_ascii_lowercase())
                .collect(),
        );
        let shutdown = Arc::new(AtomicBool::new(false));
        let shutdown_for_thread = Arc::clone(&shutdown);
        let active = Arc::new(AtomicUsize::new(0));

        let accept_handle = std::thread::spawn(move || {
            accept_loop(
                listener,
                allowed,
                shutdown_for_thread,
                active,
                allow_local_dest,
            );
        });

        Ok(Self {
            socket_path: socket_path.to_path_buf(),
            shutdown,
            accept_handle: Some(accept_handle),
        })
    }

    pub fn socket_path(&self) -> &Path {
        &self.socket_path
    }

    /// Stops the accept loop and joins its thread. Used by tests, which need
    /// deterministic teardown between cases rather than a leaked thread per
    /// test.
    pub fn shutdown(mut self) {
        self.shutdown.store(true, Ordering::SeqCst);
        if let Some(handle) = self.accept_handle.take() {
            let _ = handle.join();
        }
    }

    /// Intentionally leaks the accept thread: the proxy keeps running for
    /// the rest of the process's life instead of stopping when this value
    /// goes out of scope. This is what `linux::launch` calls for a real
    /// sandboxed task, whose lifetime is unrelated to `launch()`'s own call
    /// stack.
    ///
    /// ponytail: no per-task teardown yet -- every `AllowHosts` launch's
    /// proxy thread and socket file live until the desktop app process
    /// exits. Wiring real cleanup needs the caller to notice the launched
    /// `Child` has exited (a `wait()`/poller, mirroring
    /// `apps/control-plane/internal/agenttask.Poller`'s pattern
    /// server-side) and call `shutdown()` then. Add that once local tasks
    /// track completion (see `apps/desktop/src-tauri/src/local_tasks.rs`).
    pub fn leak_for_process_lifetime(self) {
        std::mem::forget(self);
    }
}

impl Drop for AllowlistProxy {
    fn drop(&mut self) {
        self.shutdown.store(true, Ordering::SeqCst);
        if let Some(handle) = self.accept_handle.take() {
            let _ = handle.join();
        }
    }
}

/// Decrements the active-connection counter when a handler finishes,
/// however it exits (return, error, or panic unwinds through it). Paired
/// with the `fetch_add` in `accept_loop`.
struct ActiveGuard(Arc<AtomicUsize>);

impl Drop for ActiveGuard {
    fn drop(&mut self) {
        self.0.fetch_sub(1, Ordering::SeqCst);
    }
}

fn accept_loop(
    listener: UnixListener,
    allowed: Arc<HashSet<String>>,
    shutdown: Arc<AtomicBool>,
    active: Arc<AtomicUsize>,
    allow_local_dest: bool,
) {
    while !shutdown.load(Ordering::SeqCst) {
        match listener.accept() {
            Ok((stream, _addr)) => {
                // Reserve a slot; roll back and refuse if over the ceiling.
                let prior = active.fetch_add(1, Ordering::SeqCst);
                if prior >= MAX_CONCURRENT_CONNECTIONS {
                    active.fetch_sub(1, Ordering::SeqCst);
                    let _ = write_status(&stream, "503 Service Unavailable");
                    continue;
                }
                let allowed = Arc::clone(&allowed);
                let guard = ActiveGuard(Arc::clone(&active));
                std::thread::spawn(move || {
                    let _guard = guard; // decrements on scope exit
                    let _ = handle_connection(stream, &allowed, allow_local_dest);
                });
            }
            Err(e) if e.kind() == std::io::ErrorKind::WouldBlock => {
                std::thread::sleep(ACCEPT_POLL_INTERVAL);
            }
            Err(_) => {
                // Listener itself is broken (e.g. socket file removed out
                // from under us). Nothing left to accept; stop rather than
                // spin.
                return;
            }
        }
    }
}

/// Handles one CONNECT request end to end: parse, allowlist check, DNS
/// resolve-and-pin, dial, "200 Connection Established", relay. Any early
/// return before "200" means the caller sees a non-2xx status line and the
/// connection is closed without ever dialing out -- the deny-by-default
/// behaviour issue #311's acceptance check exercises.
fn handle_connection(
    stream: UnixStream,
    allowed: &HashSet<String>,
    allow_local_dest: bool,
) -> std::io::Result<()> {
    // Bound the header-reading phase in time so a silent client cannot hold
    // this handler thread open. Set on the socket before cloning; the clone
    // shares the same underlying socket and therefore the same timeout.
    stream.set_read_timeout(Some(CLIENT_HEADER_READ_TIMEOUT))?;

    let mut reader = BufReader::new(stream.try_clone()?);
    let Some(request_line) = read_line_limited(&mut reader, MAX_REQUEST_LINE_BYTES)? else {
        write_status(&stream, "400 Bad Request")?;
        return Ok(());
    };
    let Some(target) = parse_connect_target(&request_line) else {
        write_status(&stream, "400 Bad Request")?;
        return Ok(());
    };
    consume_headers(&mut reader)?;

    if !is_allowed(&target.host, allowed) {
        write_status(&stream, "403 Forbidden")?;
        return Ok(());
    }

    let pinned = match resolve_pinned(&target.host, target.port, allow_local_dest) {
        Ok(addr) => addr,
        Err(_) => {
            write_status(&stream, "502 Bad Gateway")?;
            return Ok(());
        }
    };
    let dest = match TcpStream::connect(pinned) {
        Ok(d) => d,
        Err(_) => {
            write_status(&stream, "502 Bad Gateway")?;
            return Ok(());
        }
    };

    write_status(&stream, "200 Connection Established")?;
    // The tunnel may idle far longer than the header read timeout (an LLM
    // streaming a slow response). Clear the deadline before relaying so a
    // legitimate quiet tunnel is not torn down.
    stream.set_read_timeout(None)?;
    relay(stream, dest)
}

struct ConnectTarget {
    host: String,
    port: u16,
}

/// Parses a CONNECT request line ("CONNECT host:port HTTP/1.1"). Returns
/// `None` for anything else (malformed line, or a method other than
/// CONNECT -- plain-HTTP forwarding is out of scope, see module doc).
fn parse_connect_target(line: &str) -> Option<ConnectTarget> {
    let mut parts = line.trim_end().split(' ');
    if parts.next()? != "CONNECT" {
        return None;
    }
    let hostport = parts.next()?;
    let (host, port_str) = hostport.rsplit_once(':')?;
    let port = port_str.parse().ok()?;
    Some(ConnectTarget {
        host: host.to_string(),
        port,
    })
}

fn is_allowed(host: &str, allowed: &HashSet<String>) -> bool {
    allowed.contains(&host.to_ascii_lowercase())
}

/// Resolves `host` exactly once and returns the first address that is not a
/// forbidden (private/loopback/link-local/unspecified/multicast) target, so
/// the caller dials that literal `SocketAddr` rather than triggering a
/// second, independent resolution -- the same DNS-rebind mitigation
/// `apps/agent-engine/internal/egressproxy.resolvePinnedAddr` documents,
/// plus the private-IP guard added in issue #342 item 2. If every resolved
/// address is forbidden (e.g. an allowed hostname rebound to `127.0.0.1` or
/// a `169.254.x.x`/RFC1918 address), the connection is refused rather than
/// used to reach an internal service. `allow_local_dest` opts out of the
/// guard for tests that dial a loopback echo server; production never does.
fn resolve_pinned(
    host: &str,
    port: u16,
    allow_local_dest: bool,
) -> std::io::Result<std::net::SocketAddr> {
    for addr in (host, port).to_socket_addrs()? {
        if allow_local_dest || !is_forbidden_ip(addr.ip()) {
            return Ok(addr);
        }
    }
    Err(std::io::Error::new(
        std::io::ErrorKind::PermissionDenied,
        "host resolved only to disallowed (private/loopback/link-local) addresses",
    ))
}

/// True for an address a sandboxed task must never be allowed to reach:
/// loopback, unspecified, multicast, and the private/link-local ranges on
/// both IPv4 and IPv6 (including IPv4-mapped IPv6, so `::ffff:127.0.0.1`
/// cannot slip a loopback past the IPv4 check).
fn is_forbidden_ip(ip: IpAddr) -> bool {
    match ip {
        IpAddr::V4(v4) => is_forbidden_v4(v4),
        IpAddr::V6(v6) => {
            if let Some(mapped) = v6.to_ipv4_mapped() {
                return is_forbidden_v4(mapped);
            }
            v6.is_loopback()
                || v6.is_unspecified()
                || v6.is_multicast()
                || is_ipv6_unique_local(v6)
                || is_ipv6_link_local(v6)
        }
    }
}

fn is_forbidden_v4(v4: Ipv4Addr) -> bool {
    v4.is_private()
        || v4.is_loopback()
        || v4.is_link_local()
        || v4.is_unspecified()
        || v4.is_broadcast()
        || v4.is_documentation()
        || v4.is_multicast()
}

/// `fc00::/7` unique-local (checked manually: the std predicate is unstable).
fn is_ipv6_unique_local(ip: Ipv6Addr) -> bool {
    (ip.segments()[0] & 0xfe00) == 0xfc00
}

/// `fe80::/10` link-local unicast (checked manually: std predicate unstable).
fn is_ipv6_link_local(ip: Ipv6Addr) -> bool {
    (ip.segments()[0] & 0xffc0) == 0xfe80
}

/// Reads headers up to and including the blank line terminating an HTTP
/// request, discarding their content: this proxy does not need any of
/// them, but must consume them so the byte stream that follows on the same
/// connection (the tunnelled traffic, for an allowed CONNECT) starts
/// exactly where the client expects the "200" response to have left it.
/// Each line is length-bounded and the total count is capped so a hostile
/// client cannot stream unbounded headers (issue #342 item 3).
fn consume_headers(reader: &mut BufReader<UnixStream>) -> std::io::Result<()> {
    for _ in 0..MAX_HEADER_LINES {
        let Some(line) = read_line_limited(reader, MAX_REQUEST_LINE_BYTES)? else {
            return Err(std::io::Error::new(
                std::io::ErrorKind::InvalidData,
                "header line exceeded maximum length",
            ));
        };
        if line.trim_end_matches(['\r', '\n']).is_empty() {
            return Ok(());
        }
    }
    Err(std::io::Error::new(
        std::io::ErrorKind::InvalidData,
        "too many header lines",
    ))
}

/// Reads one line, reading at most `max_bytes`. Returns `Ok(None)` when the
/// byte limit is reached before a terminating newline (an over-length or
/// never-terminated line), so the caller can reject it rather than block or
/// buffer unboundedly.
fn read_line_limited(
    reader: &mut BufReader<UnixStream>,
    max_bytes: u64,
) -> std::io::Result<Option<String>> {
    let mut line = String::new();
    let n = (&mut *reader).take(max_bytes).read_line(&mut line)?;
    if n as u64 == max_bytes && !line.ends_with('\n') {
        return Ok(None);
    }
    Ok(Some(line))
}

fn write_status(mut stream: &UnixStream, status_line: &str) -> std::io::Result<()> {
    stream.write_all(format!("HTTP/1.1 {status_line}\r\n\r\n").as_bytes())
}

fn relay(unix: UnixStream, tcp: TcpStream) -> std::io::Result<()> {
    let mut unix_read = unix.try_clone()?;
    let mut tcp_write = tcp.try_clone()?;
    let mut unix_write = unix;
    let mut tcp_read = tcp;

    let to_dest = std::thread::spawn(move || {
        let _ = std::io::copy(&mut unix_read, &mut tcp_write);
    });
    let _ = std::io::copy(&mut tcp_read, &mut unix_write);
    let _ = to_dest.join();
    Ok(())
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::net::TcpListener;

    fn temp_socket_path(label: &str) -> PathBuf {
        std::env::temp_dir().join(format!(
            "hive-egress-proxy-test-{label}-{}-{}.sock",
            std::process::id(),
            label.len()
        ))
    }

    /// A one-shot TCP echo server: accepts one connection, echoes whatever
    /// it reads back, then exits. Enough to prove the relay actually
    /// carries bytes both ways, not just that a "200" was returned.
    fn spawn_echo_server() -> u16 {
        let listener = TcpListener::bind("127.0.0.1:0").unwrap();
        let port = listener.local_addr().unwrap().port();
        std::thread::spawn(move || {
            if let Ok((mut stream, _)) = listener.accept() {
                let mut buf = [0u8; 64];
                if let Ok(n) = stream.read(&mut buf) {
                    let _ = stream.write_all(&buf[..n]);
                }
            }
        });
        port
    }

    fn connect_and_send(socket_path: &Path, request: &str) -> String {
        let mut stream = UnixStream::connect(socket_path).unwrap();
        stream.write_all(request.as_bytes()).unwrap();
        let mut reader = BufReader::new(stream);
        let mut status_line = String::new();
        reader.read_line(&mut status_line).unwrap();
        status_line
    }

    #[test]
    fn allowed_host_gets_200_and_relays_bytes() {
        let echo_port = spawn_echo_server();
        let socket_path = temp_socket_path("allowed");
        // Loopback echo server: this test opts into local destinations, the
        // one place blocking loopback (the default) would be wrong.
        let proxy =
            AllowlistProxy::spawn_with_policy(&socket_path, vec!["127.0.0.1".to_string()], true)
                .unwrap();

        let mut stream = UnixStream::connect(&socket_path).unwrap();
        stream
            .write_all(
                format!("CONNECT 127.0.0.1:{echo_port} HTTP/1.1\r\nHost: x\r\n\r\n").as_bytes(),
            )
            .unwrap();

        let mut reader = BufReader::new(stream.try_clone().unwrap());
        let mut status_line = String::new();
        reader.read_line(&mut status_line).unwrap();
        assert!(
            status_line.starts_with("HTTP/1.1 200"),
            "unexpected status: {status_line}"
        );
        // "200 Connection Established" is a full (if header-less) HTTP
        // response: read_line only consumes the status line itself, so the
        // blank line terminating it (write_status's trailing "\r\n\r\n") is
        // still one line "\r\n" away and must be consumed too, or it
        // corrupts the raw relay bytes read next.
        let mut blank_line = String::new();
        reader.read_line(&mut blank_line).unwrap();

        stream.write_all(b"ping").unwrap();
        let mut buf = [0u8; 4];
        reader.read_exact(&mut buf).unwrap();
        assert_eq!(&buf, b"ping");

        proxy.shutdown();
    }

    #[test]
    fn host_outside_allowlist_gets_403_and_never_dials() {
        let socket_path = temp_socket_path("denied");
        // Deliberately empty allowlist: the correct fail-closed behaviour
        // for a policy that could not be resolved (see `Service.Effective`
        // in apps/control-plane/internal/egress/service.go).
        let proxy = AllowlistProxy::spawn(&socket_path, vec![]).unwrap();

        let status_line = connect_and_send(
            &socket_path,
            "CONNECT example.com:443 HTTP/1.1\r\nHost: x\r\n\r\n",
        );
        assert!(
            status_line.starts_with("HTTP/1.1 403"),
            "unexpected status: {status_line}"
        );

        proxy.shutdown();
    }

    #[test]
    fn host_match_is_case_insensitive() {
        let echo_port = spawn_echo_server();
        let socket_path = temp_socket_path("case");
        let proxy =
            AllowlistProxy::spawn_with_policy(&socket_path, vec!["127.0.0.1".to_string()], true)
                .unwrap();

        // Same host, would only differ in case for a real hostname; assert
        // the lookup itself normalizes rather than relying on 127.0.0.1
        // having case variants (it has none) -- exercise is_allowed
        // directly instead.
        let allowed: HashSet<String> = ["Example.COM".to_string()]
            .into_iter()
            .map(|h| h.to_ascii_lowercase())
            .collect();
        assert!(is_allowed("example.com", &allowed));
        assert!(is_allowed("EXAMPLE.COM", &allowed));

        let status_line = connect_and_send(
            &socket_path,
            &format!("CONNECT 127.0.0.1:{echo_port} HTTP/1.1\r\nHost: x\r\n\r\n"),
        );
        assert!(status_line.starts_with("HTTP/1.1 200"));

        proxy.shutdown();
    }

    #[test]
    fn malformed_request_line_gets_400() {
        let socket_path = temp_socket_path("malformed");
        let proxy = AllowlistProxy::spawn(&socket_path, vec![]).unwrap();

        let status_line = connect_and_send(&socket_path, "not a request\r\n\r\n");
        assert!(
            status_line.starts_with("HTTP/1.1 400"),
            "unexpected status: {status_line}"
        );

        proxy.shutdown();
    }

    #[test]
    fn non_connect_method_is_rejected() {
        let socket_path = temp_socket_path("get-method");
        let proxy = AllowlistProxy::spawn(&socket_path, vec!["example.com".to_string()]).unwrap();

        let status_line = connect_and_send(
            &socket_path,
            "GET http://example.com/ HTTP/1.1\r\nHost: example.com\r\n\r\n",
        );
        assert!(
            status_line.starts_with("HTTP/1.1 400"),
            "unexpected status: {status_line}"
        );

        proxy.shutdown();
    }

    #[test]
    fn oversized_request_line_gets_400() {
        let socket_path = temp_socket_path("oversize");
        let proxy = AllowlistProxy::spawn(&socket_path, vec![]).unwrap();

        // A CONNECT line far longer than the cap, with no terminating
        // newline within the first MAX_REQUEST_LINE_BYTES: must be rejected
        // rather than buffered unboundedly.
        let mut huge = String::from("CONNECT ");
        huge.push_str(&"a".repeat(MAX_REQUEST_LINE_BYTES as usize + 100));
        huge.push_str(":443 HTTP/1.1\r\n\r\n");

        let status_line = connect_and_send(&socket_path, &huge);
        assert!(
            status_line.starts_with("HTTP/1.1 400"),
            "unexpected status: {status_line}"
        );

        proxy.shutdown();
    }

    #[test]
    fn socket_path_returns_bound_path() {
        let socket_path = temp_socket_path("path-accessor");
        let proxy = AllowlistProxy::spawn(&socket_path, vec![]).unwrap();
        assert_eq!(proxy.socket_path(), socket_path.as_path());
        proxy.shutdown();
    }

    #[test]
    fn shutdown_stops_the_accept_loop() {
        let socket_path = temp_socket_path("shutdown");
        let proxy = AllowlistProxy::spawn(&socket_path, vec![]).unwrap();
        proxy.shutdown();

        // The accept thread has stopped, but the socket *file* may still
        // exist (bind doesn't get cleaned up by shutting down the accept
        // loop). What matters is that nothing is listening any more.
        let result = UnixStream::connect(&socket_path);
        assert!(
            result.is_err(),
            "connecting after shutdown should fail (nothing listening)"
        );
    }

    #[test]
    fn parse_connect_target_rejects_missing_port() {
        assert!(parse_connect_target("CONNECT example.com HTTP/1.1").is_none());
    }

    #[test]
    fn parse_connect_target_accepts_well_formed_line() {
        let target = parse_connect_target("CONNECT example.com:443 HTTP/1.1").unwrap();
        assert_eq!(target.host, "example.com");
        assert_eq!(target.port, 443);
    }

    #[test]
    fn resolve_pinned_rejects_private_loopback_and_link_local() {
        // Literal addresses: to_socket_addrs parses them without DNS, so
        // these assertions do no network I/O and are deterministic.
        assert!(resolve_pinned("127.0.0.1", 443, false).is_err());
        assert!(resolve_pinned("10.0.0.1", 443, false).is_err());
        assert!(resolve_pinned("192.168.1.1", 443, false).is_err());
        assert!(resolve_pinned("169.254.1.1", 443, false).is_err());
        assert!(resolve_pinned("0.0.0.0", 443, false).is_err());
    }

    #[test]
    fn resolve_pinned_allows_public_ip() {
        let addr = resolve_pinned("8.8.8.8", 443, false).unwrap();
        assert_eq!(addr.ip().to_string(), "8.8.8.8");
        assert_eq!(addr.port(), 443);
    }

    #[test]
    fn resolve_pinned_allow_local_opt_in_permits_loopback() {
        // The test-only opt-out used by the relay tests above.
        assert!(resolve_pinned("127.0.0.1", 443, true).is_ok());
    }

    #[test]
    fn is_forbidden_ip_classifies_internal_and_public() {
        for s in [
            "127.0.0.1",
            "10.1.2.3",
            "192.168.0.1",
            "172.16.0.1",
            "169.254.0.1",
            "0.0.0.0",
            "255.255.255.255",
            "224.0.0.1",
            "::1",
            "::",
            "fe80::1",
            "fc00::1",
            "fd12:3456::1",
            "::ffff:127.0.0.1",
            "::ffff:10.0.0.1",
        ] {
            assert!(
                is_forbidden_ip(s.parse::<IpAddr>().unwrap()),
                "{s} should be forbidden"
            );
        }
        for s in [
            "8.8.8.8",
            "1.1.1.1",
            "93.184.216.34",
            "2606:4700:4700::1111",
        ] {
            assert!(
                !is_forbidden_ip(s.parse::<IpAddr>().unwrap()),
                "{s} should be allowed"
            );
        }
    }
}
