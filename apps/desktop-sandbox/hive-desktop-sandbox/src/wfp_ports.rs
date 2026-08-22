//! Loopback proxy port range and the loopback-allowlist complement math for
//! the Windows WFP egress fence (Integration B2).
//!
//! This module is deliberately platform-independent (no Win32): it holds the
//! two pure pieces of the Windows egress boundary that this repository can
//! actually unit-test on its Linux CI job, exactly like `windows_plan.rs`.
//! The Win32 pieces that consume these (the firewall COM allowlist in
//! `windows_firewall.rs`, keyed on the sandbox account SID) are compiled only
//! `cfg(windows)` and lab-validated (`spike307-win`, D-004).
//!
//! CTO decision Q2 (`plan-b2-wfp-egress-2026-07-20.md`): the Hive loopback
//! proxy binds a free port inside the fixed range **62080-62089**. The WFP /
//! firewall PERMIT is scoped to loopback intersected with that range; the
//! block complement (everything on loopback TCP except the bound proxy port)
//! is computed by [`blocked_loopback_tcp_remote_ports`].

use std::net::TcpListener;
use std::ops::RangeInclusive;

/// Lowest port the Hive loopback egress proxy may bind (CTO Q2).
pub const PROXY_PORT_RANGE_LOW: u16 = 62080;
/// Highest port the Hive loopback egress proxy may bind (CTO Q2).
pub const PROXY_PORT_RANGE_HIGH: u16 = 62089;

/// The fixed loopback proxy port range, `62080..=62089`.
pub const PROXY_PORT_RANGE: RangeInclusive<u16> = PROXY_PORT_RANGE_LOW..=PROXY_PORT_RANGE_HIGH;

/// Binds the Hive loopback egress proxy to the first free `127.0.0.1` port in
/// [`PROXY_PORT_RANGE`], returning the listener and the bound port.
///
/// This is the Windows transport for the AllowlistProxy: unlike the Linux
/// path (a Unix socket bind-mounted into the bwrap netns plus the
/// `hive-egress-shim` TCP-to-unix bridge), Windows has no netns and no
/// bind-mount, so the proxy binds a loopback TCP listener directly on a port
/// inside the WFP/firewall permit range. Binding a specific in-range port
/// (not `:0`) is required so the kernel PERMIT can be scoped to it. The proxy
/// runs AS the launcher identity, not the blocked sandbox SID, so its own
/// upstream egress is unaffected.
///
/// The listener is returned unaccepted so the caller can hand it to the
/// AllowlistProxy body; this function performs no allowlist enforcement
/// itself (that lives in `egress_proxy.rs`).
pub fn bind_loopback_proxy() -> std::io::Result<(TcpListener, u16)> {
    let mut last_err = None;
    for port in PROXY_PORT_RANGE {
        match TcpListener::bind(("127.0.0.1", port)) {
            Ok(listener) => return Ok((listener, port)),
            Err(err) => last_err = Some(err),
        }
    }
    Err(last_err.unwrap_or_else(|| {
        std::io::Error::new(
            std::io::ErrorKind::AddrInUse,
            "no free loopback proxy port in range 62080-62089",
        )
    }))
}

/// Computes the loopback TCP remote-port ranges to BLOCK so that every port
/// except the given `proxy_ports` is denied. Returns `None` when there is
/// nothing to block (only possible if the entire `1..=65535` range is
/// allowed, which never happens for a real single-port proxy).
///
/// This is the complement of the loopback allowlist: the firewall carries a
/// broad loopback-TCP block and then narrows it to this port complement, so a
/// child can reach only `127.0.0.1:<proxy_port>` and no other loopback
/// listener. Port `0` is ignored (never a real bound port). Fail-closed: if
/// this returns `None` because the caller passed no usable port, the broad
/// loopback block installed before narrowing stays in force.
pub fn blocked_loopback_tcp_remote_ports(proxy_ports: &[u16]) -> Option<String> {
    let mut allowed_ports = proxy_ports
        .iter()
        .copied()
        .filter(|port| *port != 0)
        .collect::<Vec<_>>();
    allowed_ports.sort_unstable();
    allowed_ports.dedup();

    let mut blocked_ranges = Vec::new();
    let mut start = 1_u32;
    for port in allowed_ports {
        let port = u32::from(port);
        if port < start {
            continue;
        }
        if port > start {
            blocked_ranges.push(port_range_string(start, port - 1));
        }
        start = port + 1;
    }

    if start <= u32::from(u16::MAX) {
        blocked_ranges.push(port_range_string(start, u32::from(u16::MAX)));
    }

    if blocked_ranges.is_empty() {
        None
    } else {
        Some(blocked_ranges.join(","))
    }
}

fn port_range_string(start: u32, end: u32) -> String {
    if start == end {
        start.to_string()
    } else {
        format!("{start}-{end}")
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use pretty_assertions::assert_eq;

    #[test]
    fn proxy_port_range_is_the_locked_cto_q2_range() {
        assert_eq!(PROXY_PORT_RANGE_LOW, 62080);
        assert_eq!(PROXY_PORT_RANGE_HIGH, 62089);
        assert_eq!(PROXY_PORT_RANGE.count(), 10);
    }

    #[test]
    fn bind_loopback_proxy_returns_a_port_inside_the_range() {
        let (listener, port) = bind_loopback_proxy().expect("a loopback port should be free");
        assert!(
            PROXY_PORT_RANGE.contains(&port),
            "bound port {port} must be inside 62080-62089"
        );
        assert_eq!(listener.local_addr().unwrap().port(), port);
    }

    #[test]
    fn complement_of_single_proxy_port_blocks_everything_else() {
        // Allow only 62085: block 1..=62084 and 62086..=65535.
        let blocked = blocked_loopback_tcp_remote_ports(&[62085])
            .expect("a single-port allowlist must leave ports to block");
        assert_eq!(blocked, "1-62084,62086-65535");
    }

    #[test]
    fn complement_ignores_zero_and_dedups_and_sorts() {
        let blocked = blocked_loopback_tcp_remote_ports(&[0, 62085, 62085, 62080])
            .expect("still ports to block");
        // Allowed {62080, 62085}: gaps are 1..=62079, 62081..=62084, 62086..=65535.
        assert_eq!(blocked, "1-62079,62081-62084,62086-65535");
    }

    #[test]
    fn complement_of_no_ports_blocks_the_whole_range() {
        // Blocker b / teardown regression: the task-end
        // `teardown_offline_proxy_allowlist` calls this with an empty allowlist,
        // and it must block the WHOLE loopback range (never leave a port open).
        // Empty / all-zero allowlist => block everything (fail-closed).
        assert_eq!(
            blocked_loopback_tcp_remote_ports(&[]),
            Some("1-65535".to_string())
        );
        assert_eq!(
            blocked_loopback_tcp_remote_ports(&[0]),
            Some("1-65535".to_string())
        );
    }

    #[test]
    fn complement_allowing_max_port_has_no_trailing_range() {
        let blocked = blocked_loopback_tcp_remote_ports(&[u16::MAX])
            .expect("everything below 65535 is still blocked");
        assert_eq!(blocked, "1-65534");
    }
}
