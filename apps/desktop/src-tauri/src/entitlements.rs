//! Step 4.3 (#310): desktop gate/license fetch at startup.
//!
//! ## Auth timing decision
//!
//! `main.rs`'s `setup()` runs before the window exists, so there is no
//! webview and no session: the agent-console page (which holds the real
//! Supabase session, per `settings.rs`'s doc comment on `CONSOLE_BASE_PATH`)
//! has not loaded yet. The main window's Tauri capability
//! (`src-tauri/capabilities/default.json`) also grants no IPC to remote
//! content by design, so there is no channel for that page to hand its
//! session back to Rust even after it loads.
//!
//! Given that, this module fetches `GET {origin}/v1/featuregate`
//! (`apps/edge-api/internal/featuregate/handler.go` `NewStateHandler`)
//! unauthenticated at startup. That call always comes back 401/403 today
//! (`AwaitingSession`) -- expected, not a failure. The real, authenticated
//! gate check already happens on every load of the agent-console page
//! itself, which calls the same endpoint with its own session and hides or
//! shows Cowork accordingly; nothing here needs to duplicate that. What this
//! startup fetch DOES add: telling a genuinely unreachable server (network
//! error, timeout, unexpected status) apart from a merely unauthenticated
//! one, so `main.rs` can fail safe and legible instead of silently opening a
//! window onto a dead URL.
//!
//! License state has no client-facing HTTP surface to call: the only
//! license read path, `GET /internal/license/entitlement`
//! (`apps/control-plane/internal/licensing/handler.go`), is wrapped in
//! `platformhttp.RequireInternalToken`, a service-to-service secret shared
//! between edge-api and control-plane that a desktop client must never hold
//! (and D9 already makes license state a non-gating attribute -- feature
//! gates are the sole capability switch, see `apps/control-plane/internal/
//! licensing/license.go`). So this module gates on featuregate alone.

use std::collections::HashMap;
use std::sync::Mutex;
use std::time::Duration;

use reqwest::Client;
use serde::{Deserialize, Serialize};

/// tenant_setting_key mirrored from apps/edge-api/internal/featuregate/gate.go
/// FeatureCowork. Kept as a local constant (not imported -- there is no Rust
/// binding to the Go package) so a rename on either side is a one-line diff
/// away from being caught by a test, not a silent drift.
const GATE_COWORK: &str = "ENABLE_COWORK";

const FETCH_TIMEOUT: Duration = Duration::from_secs(5);

/// Body shape of `GET {origin}/v1/featuregate`: identical to edge-api's
/// `FlagsResponse` (`{"gates": {key: bool}}`).
#[derive(Deserialize)]
struct FlagsResponse {
    gates: HashMap<String, bool>,
}

/// Result of the startup gate fetch.
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum GateStatus {
    /// The server returned gate state. Only reachable today via a 200
    /// response; kept generic (not "cowork: bool") so a future authenticated
    /// caller reusing `fetch` needs no new variant.
    Known(HashMap<String, bool>),
    /// Reachable, but the request had no session (401/403). Expected at cold
    /// startup -- see module doc. Not a failure.
    AwaitingSession,
    /// The server did not answer as a Hive server should: transport error,
    /// timeout, or an unexpected status. Distinct from `AwaitingSession` so
    /// the caller can show a real error instead of proceeding to a dead URL.
    Unreachable(String),
}

/// Startup entitlement state. Only a `Known` status with the Cowork key
/// explicitly `true` opens Cowork -- every other status, including one that
/// has never fetched, reads as disabled (fail closed).
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Entitlements {
    pub status: GateStatus,
}

impl Entitlements {
    /// State used before any fetch runs and whenever no server is configured
    /// yet (first run): not an error, just nothing to check.
    pub fn unconfigured() -> Self {
        Entitlements {
            status: GateStatus::AwaitingSession,
        }
    }

    pub fn cowork_enabled(&self) -> bool {
        matches!(&self.status, GateStatus::Known(gates) if *gates.get(GATE_COWORK).unwrap_or(&false))
    }

    pub fn is_unreachable(&self) -> bool {
        matches!(self.status, GateStatus::Unreachable(_))
    }

    fn to_view(&self) -> EntitlementsView {
        let (status, reason) = match &self.status {
            GateStatus::Known(_) => ("known", None),
            GateStatus::AwaitingSession => ("awaiting_session", None),
            GateStatus::Unreachable(reason) => ("unreachable", Some(reason.clone())),
        };
        EntitlementsView {
            status,
            cowork_enabled: self.cowork_enabled(),
            reason,
        }
    }
}

/// Frontend-facing projection of `Entitlements` for `get_entitlements`.
#[derive(Serialize)]
pub struct EntitlementsView {
    status: &'static str,
    cowork_enabled: bool,
    reason: Option<String>,
}

/// Builds the HTTP client used for the startup fetch. A dedicated builder
/// (rather than `Client::new()` inline at the call site) so the timeout
/// policy has one place to change.
pub fn build_client() -> Client {
    Client::builder()
        .timeout(FETCH_TIMEOUT)
        .build()
        // ponytail: Client::builder() only fails on TLS backend init or
        // invalid config we don't set here; treated as a startup-fatal
        // misconfiguration rather than threaded through as a runtime error.
        .expect("failed to build entitlements HTTP client")
}

/// Fetches gate state from `{origin}/v1/featuregate`, unauthenticated. See
/// the module doc for why unauthenticated is the right call here.
pub async fn fetch(client: &Client, origin: &str) -> Entitlements {
    let url = format!("{}/v1/featuregate", origin.trim_end_matches('/'));
    let resp = match client.get(&url).send().await {
        Ok(r) => r,
        Err(e) => {
            return Entitlements {
                status: GateStatus::Unreachable(e.to_string()),
            }
        }
    };

    let status = resp.status();
    if status.is_success() {
        return match resp.json::<FlagsResponse>().await {
            Ok(flags) => Entitlements {
                status: GateStatus::Known(flags.gates),
            },
            Err(e) => Entitlements {
                status: GateStatus::Unreachable(format!("malformed featuregate response: {e}")),
            },
        };
    }
    if status.as_u16() == 401 || status.as_u16() == 403 {
        return Entitlements {
            status: GateStatus::AwaitingSession,
        };
    }
    Entitlements {
        status: GateStatus::Unreachable(format!("unexpected status {status}")),
    }
}

/// Reads the entitlements state `main.rs` fetched at startup.
#[tauri::command]
pub fn get_entitlements(state: tauri::State<Mutex<Entitlements>>) -> EntitlementsView {
    state
        .inner()
        .lock()
        .expect("entitlements mutex poisoned")
        .to_view()
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::io::{Read, Write};
    use std::net::TcpListener;

    /// Spawns a one-shot HTTP server on an ephemeral port that writes a
    /// response with the given status line and body (Content-Length
    /// computed from `body`, never hand-counted) to the first connection it
    /// accepts, then returns the "http://host:port" origin to hit. One
    /// connection is all a single `fetch` call needs.
    fn mock_server(status_line: &'static str, body: &'static str) -> String {
        let listener = TcpListener::bind("127.0.0.1:0").unwrap();
        let addr = listener.local_addr().unwrap();
        std::thread::spawn(move || {
            if let Ok((mut stream, _)) = listener.accept() {
                let mut buf = [0u8; 1024];
                let _ = stream.read(&mut buf);
                let response = format!(
                    "{status_line}\r\ncontent-type: application/json\r\ncontent-length: {}\r\n\r\n{body}",
                    body.len()
                );
                let _ = stream.write_all(response.as_bytes());
            }
        });
        format!("http://{addr}")
    }

    /// Binds an ephemeral port, then immediately drops the listener so the
    /// port is guaranteed to refuse the next connection.
    fn unreachable_origin() -> String {
        let listener = TcpListener::bind("127.0.0.1:0").unwrap();
        let addr = listener.local_addr().unwrap();
        drop(listener);
        format!("http://{addr}")
    }

    // -- Entitlements::unconfigured -----------------------------------------

    #[test]
    fn unconfigured_is_awaiting_session_not_unreachable() {
        let e = Entitlements::unconfigured();
        assert_eq!(e.status, GateStatus::AwaitingSession);
        assert!(!e.is_unreachable());
        assert!(!e.cowork_enabled());
    }

    // -- cowork_enabled -------------------------------------------------------

    #[test]
    fn cowork_enabled_true_when_known_and_true() {
        let mut gates = HashMap::new();
        gates.insert("ENABLE_COWORK".to_string(), true);
        let e = Entitlements {
            status: GateStatus::Known(gates),
        };
        assert!(e.cowork_enabled());
    }

    #[test]
    fn cowork_enabled_false_when_known_but_false() {
        let mut gates = HashMap::new();
        gates.insert("ENABLE_COWORK".to_string(), false);
        gates.insert("ENABLE_RAG".to_string(), true);
        let e = Entitlements {
            status: GateStatus::Known(gates),
        };
        assert!(!e.cowork_enabled());
    }

    #[test]
    fn cowork_enabled_false_when_key_missing() {
        let e = Entitlements {
            status: GateStatus::Known(HashMap::new()),
        };
        assert!(!e.cowork_enabled());
    }

    #[test]
    fn cowork_enabled_false_when_awaiting_session() {
        let e = Entitlements {
            status: GateStatus::AwaitingSession,
        };
        assert!(!e.cowork_enabled());
    }

    #[test]
    fn cowork_enabled_false_when_unreachable() {
        let e = Entitlements {
            status: GateStatus::Unreachable("boom".to_string()),
        };
        assert!(!e.cowork_enabled());
        assert!(e.is_unreachable());
    }

    // -- fetch: success --------------------------------------------------------

    #[tokio::test]
    async fn fetch_parses_200_gates_body() {
        let origin = mock_server("HTTP/1.1 200 OK", "{\"gates\":{\"ENABLE_COWORK\":true}}");
        let client = build_client();
        let e = fetch(&client, &origin).await;
        match &e.status {
            GateStatus::Known(gates) => assert_eq!(gates.get("ENABLE_COWORK"), Some(&true)),
            other => panic!("expected Known, got {other:?}"),
        }
        assert!(e.cowork_enabled());
    }

    #[tokio::test]
    async fn fetch_reports_cowork_disabled_when_gate_false() {
        let origin = mock_server("HTTP/1.1 200 OK", "{\"gates\":{\"ENABLE_COWORK\":false}}");
        let client = build_client();
        let e = fetch(&client, &origin).await;
        match &e.status {
            GateStatus::Known(gates) => assert_eq!(gates.get("ENABLE_COWORK"), Some(&false)),
            other => panic!("expected Known, got {other:?}"),
        }
        assert!(!e.cowork_enabled());
    }

    // -- fetch: fail-safe branches ----------------------------------------------

    #[tokio::test]
    async fn fetch_treats_401_as_awaiting_session() {
        let origin = mock_server("HTTP/1.1 401 Unauthorized", "");
        let client = build_client();
        let e = fetch(&client, &origin).await;
        assert_eq!(e.status, GateStatus::AwaitingSession);
        assert!(!e.is_unreachable());
    }

    #[tokio::test]
    async fn fetch_treats_403_as_awaiting_session() {
        let origin = mock_server("HTTP/1.1 403 Forbidden", "");
        let client = build_client();
        let e = fetch(&client, &origin).await;
        assert_eq!(e.status, GateStatus::AwaitingSession);
    }

    #[tokio::test]
    async fn fetch_treats_500_as_unreachable() {
        let origin = mock_server("HTTP/1.1 500 Internal Server Error", "");
        let client = build_client();
        let e = fetch(&client, &origin).await;
        assert!(e.is_unreachable());
    }

    #[tokio::test]
    async fn fetch_treats_malformed_json_body_as_unreachable() {
        let origin = mock_server("HTTP/1.1 200 OK", "not json!");
        let client = build_client();
        let e = fetch(&client, &origin).await;
        assert!(e.is_unreachable());
        assert!(!e.cowork_enabled());
    }

    #[tokio::test]
    async fn fetch_treats_connection_refused_as_unreachable() {
        let origin = unreachable_origin();
        let client = build_client();
        let e = fetch(&client, &origin).await;
        assert!(e.is_unreachable());
        assert!(!e.cowork_enabled());
    }
}
