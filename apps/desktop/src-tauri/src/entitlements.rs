//! Seam for Step 4.3 (#310): desktop auth plus feature-gate/license fetch.
//!
//! Not implemented in Step 4.1. `fetch_stub` marks the exact point in
//! `main.rs`'s startup flow where the real fetch belongs: after the
//! console URL is resolved (so the fetch target is known) and before the
//! window is created (so no plugin pack surface renders until gates and
//! license state come back). Step 4.3 replaces this stub with a call to
//! the control-plane featuregate and licensing APIs (from Step 1.1 and
//! Step 1.4) and should block window creation on `gates_ok` being false.

// ponytail: gates_ok is unread until Step 4.3 wires the real fetch and a
// caller that acts on it; kept on the struct now so that caller only adds
// a branch, not a new field.
#[allow(dead_code)]
pub struct Entitlements {
    pub gates_ok: bool,
}

pub fn fetch_stub() -> Entitlements {
    Entitlements { gates_ok: true }
}
