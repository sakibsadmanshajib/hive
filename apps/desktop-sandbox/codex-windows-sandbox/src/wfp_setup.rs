//! Fail-closed WFP install wrapper.
//!
//! Ported (not vendored verbatim) from openai/codex
//! `codex-rs/windows-sandbox-rs/src/wfp_setup.rs` at commit
//! a47c661ea9e226fe65e46cf9dbc5c5ed75c2c762 (Apache-2.0). See
//! `../VENDORING.md`.
//!
//! Deviation from upstream, and the whole point of this wrapper: upstream's
//! `install_wfp_filters` treats a WFP install failure (or panic) as
//! NON-FATAL: it logs the error, emits a Statsig metric, and lets elevated
//! setup continue anyway. Hive INVERTS that to fail-closed (D-005): a WFP
//! install failure returns `Err`, and the elevated provisioning path aborts
//! rather than leaving a sandbox account whose egress fence was never
//! installed. The upstream OTEL / Statsig metric machinery is dropped (this
//! crate does not vendor `codex-otel`); callers get a plain log callback and
//! a `Result` instead.

use crate::wfp::install_wfp_filters_for_account;
use anyhow::Result;

/// Installs the persistent Hive WFP egress filters for `account`, returning
/// the number of filters installed on success.
///
/// Fail-closed: any error from the underlying WFP transaction is returned to
/// the caller unchanged so provisioning aborts. `log` receives one human
/// readable line describing the outcome (success count, or the failure and
/// that provisioning is aborting).
pub fn install_wfp_filters<F>(account: &str, mut log: F) -> Result<usize>
where
    F: FnMut(&str),
{
    match install_wfp_filters_for_account(account) {
        Ok(installed_filter_count) => {
            log(&format!(
                "WFP setup installed {installed_filter_count} filters for {account}"
            ));
            Ok(installed_filter_count)
        }
        Err(err) => {
            log(&format!(
                "WFP setup FAILED for {account}: {err}; aborting provisioning (fail-closed, D-005)"
            ));
            Err(err)
        }
    }
}
