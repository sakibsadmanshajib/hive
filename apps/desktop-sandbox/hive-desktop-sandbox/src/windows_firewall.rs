//! Windows Firewall COM layer of the two-layer egress fence (Integration B2,
//! CTO decision Q1: keep the Codex two-layer model, WFP per-protocol blocks
//! plus this firewall block-all + loopback allowlist, both keyed on the
//! sandbox account SID).
//!
//! Ported (Hive-authored, not vendored verbatim) from openai/codex
//! `codex-rs/windows-sandbox-rs/src/bin/setup_main/win/firewall.rs` at commit
//! a47c661ea9e226fe65e46cf9dbc5c5ed75c2c762 (Apache-2.0). See `../VENDORING.md`
//! for the deviation record. Deviations from upstream:
//! - rule name constants renamed `codex_sandbox_offline_*` ->
//!   `hive_sandbox_offline_*`;
//! - upstream's `SetupErrorCode`/`SetupFailure` taxonomy replaced with plain
//!   `anyhow` errors (this is a Hive-authored port, not the Codex setup
//!   binary), while preserving fail-closed behaviour: every COM failure and
//!   the SID read-back mismatch still return `Err`;
//! - the loopback-allowlist port complement is computed by the shared,
//!   Linux-testable [`crate::wfp_ports::blocked_loopback_tcp_remote_ports`]
//!   rather than a private copy;
//! - the `chrono` RFC3339 log timestamp is dropped (the caller's log sink owns
//!   timestamps).
//!
//! Applied only `cfg(windows)`. It is NOT wired into `launch()` in this
//! (inert) B2 PR: `launch()` still refuses both network policies. Activation
//! is a separate lab-gated PR (D-004).

use crate::wfp_ports::blocked_loopback_tcp_remote_ports;
use anyhow::Result;
use anyhow::anyhow;
use std::io::Write;

use windows::Win32::Foundation::S_OK;
use windows::Win32::Foundation::VARIANT_TRUE;
use windows::Win32::NetworkManagement::WindowsFirewall::INetFwPolicy2;
use windows::Win32::NetworkManagement::WindowsFirewall::INetFwRule3;
use windows::Win32::NetworkManagement::WindowsFirewall::INetFwRules;
use windows::Win32::NetworkManagement::WindowsFirewall::NET_FW_ACTION_BLOCK;
use windows::Win32::NetworkManagement::WindowsFirewall::NET_FW_IP_PROTOCOL_ANY;
use windows::Win32::NetworkManagement::WindowsFirewall::NET_FW_IP_PROTOCOL_TCP;
use windows::Win32::NetworkManagement::WindowsFirewall::NET_FW_IP_PROTOCOL_UDP;
use windows::Win32::NetworkManagement::WindowsFirewall::NET_FW_MODIFY_STATE;
use windows::Win32::NetworkManagement::WindowsFirewall::NET_FW_MODIFY_STATE_OK;
use windows::Win32::NetworkManagement::WindowsFirewall::NET_FW_PROFILE2_ALL;
use windows::Win32::NetworkManagement::WindowsFirewall::NET_FW_RULE_DIR_OUT;
use windows::Win32::NetworkManagement::WindowsFirewall::NetFwPolicy2;
use windows::Win32::NetworkManagement::WindowsFirewall::NetFwRule;
use windows::Win32::System::Com::CLSCTX_INPROC_SERVER;
use windows::Win32::System::Com::COINIT_APARTMENTTHREADED;
use windows::Win32::System::Com::CoCreateInstance;
use windows::Win32::System::Com::CoInitializeEx;
use windows::Win32::System::Com::CoUninitialize;
use windows::core::BSTR;
use windows::core::Interface;

// Stable identifiers used to find/update rules idempotently. They intentionally
// do not change between installs.
const OFFLINE_BLOCK_RULE_NAME: &str = "hive_sandbox_offline_block_outbound";
const OFFLINE_BLOCK_LOOPBACK_TCP_RULE_NAME: &str = "hive_sandbox_offline_block_loopback_tcp";
const OFFLINE_BLOCK_LOOPBACK_UDP_RULE_NAME: &str = "hive_sandbox_offline_block_loopback_udp";
const OFFLINE_PROXY_ALLOW_RULE_NAME: &str = "hive_sandbox_offline_allow_loopback_proxy";

// Friendly text shown in the firewall UI.
const OFFLINE_BLOCK_RULE_FRIENDLY: &str = "Hive Sandbox Offline - Block Non-Loopback Outbound";
const OFFLINE_BLOCK_LOOPBACK_TCP_RULE_FRIENDLY: &str =
    "Hive Sandbox Offline - Block Loopback TCP (Except Proxy)";
const OFFLINE_BLOCK_LOOPBACK_UDP_RULE_FRIENDLY: &str = "Hive Sandbox Offline - Block Loopback UDP";

const LOOPBACK_REMOTE_ADDRESSES: &str = "127.0.0.0/8,::/127";
const NON_LOOPBACK_REMOTE_ADDRESSES: &str = "0.0.0.0-126.255.255.255,128.0.0.0-255.255.255.255,::,::2-ffff:ffff:ffff:ffff:ffff:ffff:ffff:ffff";

struct BlockRuleSpec<'a> {
    internal_name: &'a str,
    friendly_desc: &'a str,
    protocol: i32,
    local_user_spec: &'a str,
    offline_sid: &'a str,
    remote_addresses: Option<&'a str>,
    remote_ports: Option<&'a str>,
}

/// Installs / updates the loopback allowlist: blocks all loopback UDP and all
/// loopback TCP except the `proxy_ports` (the bound proxy port), all scoped to
/// the sandbox account SID. When `allow_local_binding` is true the loopback
/// blocks are removed instead (DenyAll passes an empty `proxy_ports` with
/// `allow_local_binding = false` to block loopback outright).
pub fn ensure_offline_proxy_allowlist(
    offline_sid: &str,
    proxy_ports: &[u16],
    allow_local_binding: bool,
    log: &mut dyn Write,
) -> Result<()> {
    let local_user_spec = format!("O:LSD:(A;;CC;;;{offline_sid})");

    let hr = unsafe { CoInitializeEx(None, COINIT_APARTMENTTHREADED) };
    if hr.is_err() {
        return Err(anyhow!("CoInitializeEx failed: {hr:?}"));
    }

    let result = (|| -> Result<()> {
        let policy: INetFwPolicy2 =
            unsafe { CoCreateInstance(&NetFwPolicy2, None, CLSCTX_INPROC_SERVER) }
                .map_err(|err| anyhow!("CoCreateInstance NetFwPolicy2 failed: {err:?}"))?;
        ensure_local_policy_rules_take_effect(&policy)?;
        let rules = unsafe { policy.Rules() }
            .map_err(|err| anyhow!("INetFwPolicy2::Rules failed: {err:?}"))?;

        if allow_local_binding {
            // Remove the loopback blocks so unrestricted local binding is
            // possible again; drop the stale proxy exception too.
            remove_rule_if_present(&rules, OFFLINE_PROXY_ALLOW_RULE_NAME, log)?;
            remove_rule_if_present(&rules, OFFLINE_BLOCK_LOOPBACK_UDP_RULE_NAME, log)?;
            remove_rule_if_present(&rules, OFFLINE_BLOCK_LOOPBACK_TCP_RULE_NAME, log)?;
            return Ok(());
        }

        ensure_block_rule(
            &rules,
            &BlockRuleSpec {
                internal_name: OFFLINE_BLOCK_LOOPBACK_UDP_RULE_NAME,
                friendly_desc: OFFLINE_BLOCK_LOOPBACK_UDP_RULE_FRIENDLY,
                protocol: NET_FW_IP_PROTOCOL_UDP.0,
                local_user_spec: &local_user_spec,
                offline_sid,
                remote_addresses: Some(LOOPBACK_REMOTE_ADDRESSES),
                remote_ports: None,
            },
            log,
        )?;

        // Install a broad TCP loopback block before narrowing it to the allowed
        // proxy-port complement. If the narrowing update fails, the sandbox
        // stays fail-closed (all loopback TCP blocked).
        ensure_block_rule(
            &rules,
            &BlockRuleSpec {
                internal_name: OFFLINE_BLOCK_LOOPBACK_TCP_RULE_NAME,
                friendly_desc: OFFLINE_BLOCK_LOOPBACK_TCP_RULE_FRIENDLY,
                protocol: NET_FW_IP_PROTOCOL_TCP.0,
                local_user_spec: &local_user_spec,
                offline_sid,
                remote_addresses: Some(LOOPBACK_REMOTE_ADDRESSES),
                remote_ports: None,
            },
            log,
        )?;

        // Remove the legacy overlapping allow rule only after the explicit
        // block rules are in place so transitions do not fail open.
        remove_rule_if_present(&rules, OFFLINE_PROXY_ALLOW_RULE_NAME, log)?;

        if let Some(blocked_remote_ports) = blocked_loopback_tcp_remote_ports(proxy_ports) {
            ensure_block_rule(
                &rules,
                &BlockRuleSpec {
                    internal_name: OFFLINE_BLOCK_LOOPBACK_TCP_RULE_NAME,
                    friendly_desc: OFFLINE_BLOCK_LOOPBACK_TCP_RULE_FRIENDLY,
                    protocol: NET_FW_IP_PROTOCOL_TCP.0,
                    local_user_spec: &local_user_spec,
                    offline_sid,
                    remote_addresses: Some(LOOPBACK_REMOTE_ADDRESSES),
                    remote_ports: Some(&blocked_remote_ports),
                },
                log,
            )?;
        }
        Ok(())
    })();

    unsafe {
        CoUninitialize();
    }
    result
}

/// Installs the persistent block-all-outbound rule (all IP protocols to every
/// non-loopback address) scoped to the sandbox account SID. This is the
/// block-all half of the two-layer fence.
pub fn ensure_offline_outbound_block(offline_sid: &str, log: &mut dyn Write) -> Result<()> {
    let local_user_spec = format!("O:LSD:(A;;CC;;;{offline_sid})");

    let hr = unsafe { CoInitializeEx(None, COINIT_APARTMENTTHREADED) };
    if hr.is_err() {
        return Err(anyhow!("CoInitializeEx failed: {hr:?}"));
    }

    let result = (|| -> Result<()> {
        let policy: INetFwPolicy2 =
            unsafe { CoCreateInstance(&NetFwPolicy2, None, CLSCTX_INPROC_SERVER) }
                .map_err(|err| anyhow!("CoCreateInstance NetFwPolicy2 failed: {err:?}"))?;
        ensure_local_policy_rules_take_effect(&policy)?;
        let rules = unsafe { policy.Rules() }
            .map_err(|err| anyhow!("INetFwPolicy2::Rules failed: {err:?}"))?;

        ensure_block_rule(
            &rules,
            &BlockRuleSpec {
                internal_name: OFFLINE_BLOCK_RULE_NAME,
                friendly_desc: OFFLINE_BLOCK_RULE_FRIENDLY,
                protocol: NET_FW_IP_PROTOCOL_ANY.0,
                local_user_spec: &local_user_spec,
                offline_sid,
                remote_addresses: Some(NON_LOOPBACK_REMOTE_ADDRESSES),
                remote_ports: None,
            },
            log,
        )?;
        Ok(())
    })();

    unsafe {
        CoUninitialize();
    }
    result
}

fn remove_rule_if_present(
    rules: &INetFwRules,
    internal_name: &str,
    log: &mut dyn Write,
) -> Result<()> {
    let name = BSTR::from(internal_name);
    if unsafe { rules.Item(&name) }.is_ok() {
        unsafe { rules.Remove(&name) }
            .map_err(|err| anyhow!("Rules::Remove failed for {internal_name}: {err:?}"))?;
        log_line(log, &format!("firewall rule removed name={internal_name}"))?;
    }
    Ok(())
}

fn ensure_local_policy_rules_take_effect(policy: &INetFwPolicy2) -> Result<()> {
    let mut modify_state = NET_FW_MODIFY_STATE::default();
    let result = unsafe {
        (Interface::vtable(policy).LocalPolicyModifyState)(
            Interface::as_raw(policy),
            &mut modify_state,
        )
    };
    validate_local_policy_modify_result(result, modify_state)
}

fn validate_local_policy_modify_result(
    result: windows::core::HRESULT,
    modify_state: NET_FW_MODIFY_STATE,
) -> Result<()> {
    if result.is_err() {
        // The COM query itself failed, so Windows never gave us a policy answer.
        return Err(anyhow!(
            "INetFwPolicy2::LocalPolicyModifyState failed: {result:?}"
        ));
    }

    if result != S_OK {
        // S_FALSE means the answer only holds for some active profiles.
        return Err(anyhow!(
            "local firewall policy modifications do not apply to every current profile: LocalPolicyModifyState result={result:?}"
        ));
    }

    if modify_state == NET_FW_MODIFY_STATE_OK {
        return Ok(());
    }

    // Windows answered uniformly, and that answer says local rule edits are
    // ineffective (e.g. a group-policy override).
    Err(anyhow!(
        "local firewall policy modifications will not take effect: LocalPolicyModifyState={modify_state:?}"
    ))
}

fn ensure_block_rule(
    rules: &INetFwRules,
    spec: &BlockRuleSpec<'_>,
    log: &mut dyn Write,
) -> Result<()> {
    let name = BSTR::from(spec.internal_name);
    let rule: INetFwRule3 = match unsafe { rules.Item(&name) } {
        Ok(existing) => existing
            .cast()
            .map_err(|err| anyhow!("cast existing firewall rule to INetFwRule3 failed: {err:?}"))?,
        Err(_) => {
            let new_rule: INetFwRule3 =
                unsafe { CoCreateInstance(&NetFwRule, None, CLSCTX_INPROC_SERVER) }
                    .map_err(|err| anyhow!("CoCreateInstance NetFwRule failed: {err:?}"))?;
            unsafe { new_rule.SetName(&name) }.map_err(|err| anyhow!("SetName failed: {err:?}"))?;
            // Set all properties before adding so we never leave a
            // half-configured rule.
            configure_rule(&new_rule, spec)?;
            unsafe { rules.Add(&new_rule) }.map_err(|err| anyhow!("Rules::Add failed: {err:?}"))?;
            new_rule
        }
    };

    // Always re-apply fields to keep the setup idempotent.
    configure_rule(&rule, spec)?;

    let remote_addresses_log = spec.remote_addresses.unwrap_or("*");
    let remote_ports_log = spec.remote_ports.unwrap_or("*");
    log_line(
        log,
        &format!(
            "firewall rule configured name={} protocol={} RemoteAddresses={remote_addresses_log} RemotePorts={remote_ports_log} LocalUserAuthorizedList={}",
            spec.internal_name, spec.protocol, spec.local_user_spec
        ),
    )?;
    Ok(())
}

fn configure_rule(rule: &INetFwRule3, spec: &BlockRuleSpec<'_>) -> Result<()> {
    unsafe {
        rule.SetDescription(&BSTR::from(spec.friendly_desc))
            .map_err(|err| anyhow!("SetDescription failed: {err:?}"))?;
        rule.SetDirection(NET_FW_RULE_DIR_OUT)
            .map_err(|err| anyhow!("SetDirection failed: {err:?}"))?;
        rule.SetAction(NET_FW_ACTION_BLOCK)
            .map_err(|err| anyhow!("SetAction failed: {err:?}"))?;
        rule.SetEnabled(VARIANT_TRUE)
            .map_err(|err| anyhow!("SetEnabled failed: {err:?}"))?;
        rule.SetProfiles(NET_FW_PROFILE2_ALL.0)
            .map_err(|err| anyhow!("SetProfiles failed: {err:?}"))?;
        configure_rule_network_scope(rule, spec)?;
        rule.SetLocalUserAuthorizedList(&BSTR::from(spec.local_user_spec))
            .map_err(|err| anyhow!("SetLocalUserAuthorizedList failed: {err:?}"))?;
    }

    // Read-back verification: fail-closed if we did not actually write the
    // expected SID scope, so a rule can never silently apply to every user.
    let actual = unsafe { rule.LocalUserAuthorizedList() }
        .map_err(|err| anyhow!("LocalUserAuthorizedList (read-back) failed: {err:?}"))?;
    let actual_str = actual.to_string();
    if !actual_str.contains(spec.offline_sid) {
        return Err(anyhow!(
            "offline firewall rule user scope mismatch: expected SID {}, got {actual_str}",
            spec.offline_sid
        ));
    }
    Ok(())
}

fn configure_rule_network_scope(rule: &INetFwRule3, spec: &BlockRuleSpec<'_>) -> Result<()> {
    unsafe {
        rule.SetProtocol(spec.protocol)
            .map_err(|err| anyhow!("SetProtocol failed: {err:?}"))?;
        let remote_addresses = spec.remote_addresses.unwrap_or("*");
        rule.SetRemoteAddresses(&BSTR::from(remote_addresses))
            .map_err(|err| anyhow!("SetRemoteAddresses failed: {err:?}"))?;
        if let Some(remote_ports) = spec.remote_ports {
            rule.SetRemotePorts(&BSTR::from(remote_ports))
                .map_err(|err| anyhow!("SetRemotePorts failed: {err:?}"))?;
        }
    }
    Ok(())
}

fn log_line(log: &mut dyn Write, msg: &str) -> Result<()> {
    writeln!(log, "{msg}")?;
    Ok(())
}

#[cfg(test)]
mod tests {
    use super::*;
    use windows::Win32::Foundation::S_FALSE;
    use windows::Win32::NetworkManagement::WindowsFirewall::NET_FW_MODIFY_STATE_GP_OVERRIDE;

    #[test]
    fn local_policy_modify_state_accepts_effective_policy() {
        assert!(validate_local_policy_modify_result(S_OK, NET_FW_MODIFY_STATE_OK).is_ok());
    }

    #[test]
    fn local_policy_modify_state_rejects_ineffective_policy() {
        validate_local_policy_modify_result(S_OK, NET_FW_MODIFY_STATE_GP_OVERRIDE)
            .expect_err("group-policy override should fail sandbox firewall setup");
    }

    #[test]
    fn local_policy_modify_state_rejects_partial_profile_coverage() {
        validate_local_policy_modify_result(S_FALSE, NET_FW_MODIFY_STATE_OK)
            .expect_err("partial profile coverage should fail sandbox firewall setup");
    }
}
