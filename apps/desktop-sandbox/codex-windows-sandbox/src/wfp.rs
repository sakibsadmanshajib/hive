// Vendored from openai/codex codex-rs/windows-sandbox-rs/src/wfp.rs at commit
// a47c661ea9e226fe65e46cf9dbc5c5ed75c2c762 (Apache-2.0). Deviations from
// upstream (see ../VENDORING.md): the session / provider / sublayer display
// names are renamed to a Hive identity; PROVIDER_KEY and SUBLAYER_KEY are
// freshly minted Hive-owned GUIDs (never Codex's, so a Hive install and a
// Codex install on the same box do not share a WFP namespace); and `to_wide`
// is imported from `crate::winutil` (this crate keeps it there, not at the
// crate root as upstream does). The FFI body is otherwise unchanged.
mod filter_specs;

use crate::winutil::to_wide;
use anyhow::Result;
use std::ffi::OsStr;
use std::mem::zeroed;
use std::ptr::null;
use std::ptr::null_mut;
use windows_sys::Win32::Foundation::FWP_E_ALREADY_EXISTS;
use windows_sys::Win32::Foundation::FWP_E_FILTER_NOT_FOUND;
use windows_sys::Win32::Foundation::FWP_E_NOT_FOUND;
use windows_sys::Win32::Foundation::HANDLE;
use windows_sys::Win32::Foundation::HLOCAL;
use windows_sys::Win32::Foundation::LocalFree;
use windows_sys::Win32::NetworkManagement::WindowsFilteringPlatform::FWP_ACTION_BLOCK;
use windows_sys::Win32::NetworkManagement::WindowsFilteringPlatform::FWP_ACTION_PERMIT;
use windows_sys::Win32::NetworkManagement::WindowsFilteringPlatform::FWP_ACTRL_MATCH_FILTER;
use windows_sys::Win32::NetworkManagement::WindowsFilteringPlatform::FWP_BYTE_BLOB;
use windows_sys::Win32::NetworkManagement::WindowsFilteringPlatform::FWP_CONDITION_VALUE0;
use windows_sys::Win32::NetworkManagement::WindowsFilteringPlatform::FWP_CONDITION_VALUE0_0;
use windows_sys::Win32::NetworkManagement::WindowsFilteringPlatform::FWP_EMPTY;
use windows_sys::Win32::NetworkManagement::WindowsFilteringPlatform::FWP_MATCH_EQUAL;
use windows_sys::Win32::NetworkManagement::WindowsFilteringPlatform::FWP_MATCH_RANGE;
use windows_sys::Win32::NetworkManagement::WindowsFilteringPlatform::FWP_RANGE_TYPE;
use windows_sys::Win32::NetworkManagement::WindowsFilteringPlatform::FWP_RANGE0;
use windows_sys::Win32::NetworkManagement::WindowsFilteringPlatform::FWP_SECURITY_DESCRIPTOR_TYPE;
use windows_sys::Win32::NetworkManagement::WindowsFilteringPlatform::FWP_UINT8;
use windows_sys::Win32::NetworkManagement::WindowsFilteringPlatform::FWP_UINT16;
use windows_sys::Win32::NetworkManagement::WindowsFilteringPlatform::FWP_UINT64;
use windows_sys::Win32::NetworkManagement::WindowsFilteringPlatform::FWP_V4_ADDR_AND_MASK;
use windows_sys::Win32::NetworkManagement::WindowsFilteringPlatform::FWP_V4_ADDR_MASK;
use windows_sys::Win32::NetworkManagement::WindowsFilteringPlatform::FWP_V6_ADDR_AND_MASK;
use windows_sys::Win32::NetworkManagement::WindowsFilteringPlatform::FWP_V6_ADDR_MASK;
use windows_sys::Win32::NetworkManagement::WindowsFilteringPlatform::FWP_VALUE0;
use windows_sys::Win32::NetworkManagement::WindowsFilteringPlatform::FWP_VALUE0_0;
use windows_sys::Win32::NetworkManagement::WindowsFilteringPlatform::FWPM_ACTION0;
use windows_sys::Win32::NetworkManagement::WindowsFilteringPlatform::FWPM_ACTION0_0;
use windows_sys::Win32::NetworkManagement::WindowsFilteringPlatform::FWPM_CONDITION_ALE_USER_ID;
use windows_sys::Win32::NetworkManagement::WindowsFilteringPlatform::FWPM_CONDITION_IP_PROTOCOL;
use windows_sys::Win32::NetworkManagement::WindowsFilteringPlatform::FWPM_CONDITION_IP_REMOTE_ADDRESS;
use windows_sys::Win32::NetworkManagement::WindowsFilteringPlatform::FWPM_CONDITION_IP_REMOTE_PORT;
use windows_sys::Win32::NetworkManagement::WindowsFilteringPlatform::FWPM_DISPLAY_DATA0;
use windows_sys::Win32::NetworkManagement::WindowsFilteringPlatform::FWPM_FILTER_CONDITION0;
use windows_sys::Win32::NetworkManagement::WindowsFilteringPlatform::FWPM_FILTER_FLAG_PERSISTENT;
use windows_sys::Win32::NetworkManagement::WindowsFilteringPlatform::FWPM_FILTER0;
use windows_sys::Win32::NetworkManagement::WindowsFilteringPlatform::FWPM_FILTER0_0;
use windows_sys::Win32::NetworkManagement::WindowsFilteringPlatform::FWPM_PROVIDER_FLAG_PERSISTENT;
use windows_sys::Win32::NetworkManagement::WindowsFilteringPlatform::FWPM_PROVIDER0;
use windows_sys::Win32::NetworkManagement::WindowsFilteringPlatform::FWPM_SESSION0;
use windows_sys::Win32::NetworkManagement::WindowsFilteringPlatform::FWPM_SUBLAYER_FLAG_PERSISTENT;
use windows_sys::Win32::NetworkManagement::WindowsFilteringPlatform::FWPM_SUBLAYER0;
use windows_sys::Win32::NetworkManagement::WindowsFilteringPlatform::FwpmEngineClose0;
use windows_sys::Win32::NetworkManagement::WindowsFilteringPlatform::FwpmEngineOpen0;
use windows_sys::Win32::NetworkManagement::WindowsFilteringPlatform::FwpmFilterAdd0;
use windows_sys::Win32::NetworkManagement::WindowsFilteringPlatform::FwpmFilterDeleteByKey0;
use windows_sys::Win32::NetworkManagement::WindowsFilteringPlatform::FwpmProviderAdd0;
use windows_sys::Win32::NetworkManagement::WindowsFilteringPlatform::FwpmSubLayerAdd0;
use windows_sys::Win32::NetworkManagement::WindowsFilteringPlatform::FwpmTransactionAbort0;
use windows_sys::Win32::NetworkManagement::WindowsFilteringPlatform::FwpmTransactionBegin0;
use windows_sys::Win32::NetworkManagement::WindowsFilteringPlatform::FwpmTransactionCommit0;
use windows_sys::Win32::Security::Authorization::BuildExplicitAccessWithNameW;
use windows_sys::Win32::Security::Authorization::BuildSecurityDescriptorW;
use windows_sys::Win32::Security::Authorization::EXPLICIT_ACCESS_W;
use windows_sys::Win32::Security::Authorization::GRANT_ACCESS;
use windows_sys::Win32::Security::PSECURITY_DESCRIPTOR;
use windows_sys::Win32::System::Rpc::RPC_C_AUTHN_DEFAULT;
use windows_sys::Win32::System::Threading::INFINITE;
use windows_sys::core::GUID;

use filter_specs::ConditionSpec;
use filter_specs::FILTER_SPECS;
use filter_specs::FilterAction;
use filter_specs::FilterSpec;

const SESSION_NAME: &str = "Hive Desktop Sandbox WFP";
const PROVIDER_NAME: &str = "Hive Desktop Sandbox WFP";
const PROVIDER_DESCRIPTION: &str = "Persistent WFP provider for Hive desktop sandbox filters";
const SUBLAYER_NAME: &str = "Hive Desktop Sandbox WFP";
const SUBLAYER_DESCRIPTION: &str = "Persistent WFP sublayer for Hive desktop sandbox filters";

// WFP identifies persistent providers, sublayers, and filters by stable GUIDs.
// These values are Hive-owned identities; do not regenerate them unless we
// intentionally want to orphan old objects and create a new WFP namespace.
const PROVIDER_KEY: GUID = GUID::from_u128(0x644854c2_dc9d_4d7f_9873_034169f826cf);
const SUBLAYER_KEY: GUID = GUID::from_u128(0xab6cb1c9_5eb1_4b71_ad24_a9d87c1e0f58);

/// Installs the persistent Hive WFP filters for `account`.
///
/// This is intended to run from the already-elevated setup/provision helper.
/// Unlike upstream, the Hive caller ([`crate::wfp_setup::install_wfp_filters`])
/// treats any returned error as FATAL to provisioning (fail-closed, D-005).
pub fn install_wfp_filters_for_account(account: &str) -> Result<usize> {
    let engine = Engine::open()?;
    let mut transaction = engine.begin_transaction()?;
    ensure_provider(engine.handle)?;
    ensure_sublayer(engine.handle)?;

    let user_condition = UserMatchCondition::for_account(account)?;
    let mut installed_filter_count = 0;
    for spec in FILTER_SPECS {
        delete_filter_if_present(engine.handle, &spec.key)?;
        add_filter(engine.handle, spec, &user_condition)?;
        installed_filter_count += 1;
    }

    transaction.commit()?;
    Ok(installed_filter_count)
}

/// Owns an open WFP engine handle and closes it on drop.
struct Engine {
    handle: HANDLE,
}

impl Engine {
    fn open() -> Result<Self> {
        let session_name = to_wide(OsStr::new(SESSION_NAME));
        let mut session: FWPM_SESSION0 = unsafe { zeroed() };
        session.displayData = FWPM_DISPLAY_DATA0 {
            name: session_name.as_ptr() as *mut _,
            description: null_mut(),
        };
        session.txnWaitTimeoutInMSec = INFINITE;

        let mut handle = HANDLE::default();
        let result = unsafe {
            FwpmEngineOpen0(
                null(),
                RPC_C_AUTHN_DEFAULT as u32,
                null(),
                &session,
                &mut handle,
            )
        };
        ensure_success(result, "FwpmEngineOpen0")?;
        Ok(Self { handle })
    }

    fn begin_transaction(&self) -> Result<Transaction<'_>> {
        let result = unsafe { FwpmTransactionBegin0(self.handle, 0) };
        ensure_success(result, "FwpmTransactionBegin0")?;
        Ok(Transaction {
            engine: self,
            committed: false,
        })
    }
}

impl Drop for Engine {
    fn drop(&mut self) {
        unsafe {
            FwpmEngineClose0(self.handle);
        }
    }
}

/// Aborts an open WFP transaction unless it was explicitly committed.
struct Transaction<'a> {
    engine: &'a Engine,
    committed: bool,
}

impl Transaction<'_> {
    fn commit(&mut self) -> Result<()> {
        let result = unsafe { FwpmTransactionCommit0(self.engine.handle) };
        ensure_success(result, "FwpmTransactionCommit0")?;
        self.committed = true;
        Ok(())
    }
}

impl Drop for Transaction<'_> {
    fn drop(&mut self) {
        if !self.committed {
            unsafe {
                FwpmTransactionAbort0(self.engine.handle);
            }
        }
    }
}

/// Builds the ALE_USER_ID condition blob that scopes filters to one account.
struct UserMatchCondition {
    security_descriptor: PSECURITY_DESCRIPTOR,
    blob: FWP_BYTE_BLOB,
}

impl UserMatchCondition {
    fn for_account(account: &str) -> Result<Self> {
        let account_w = to_wide(OsStr::new(account));
        let mut access: EXPLICIT_ACCESS_W = unsafe { zeroed() };
        unsafe {
            BuildExplicitAccessWithNameW(
                &mut access,
                account_w.as_ptr(),
                FWP_ACTRL_MATCH_FILTER,
                GRANT_ACCESS,
                0,
            );
        }

        let mut security_descriptor: PSECURITY_DESCRIPTOR = null_mut();
        let mut security_descriptor_len = 0;
        let result = unsafe {
            BuildSecurityDescriptorW(
                null(),
                null(),
                1,
                &access,
                0,
                null(),
                null_mut(),
                &mut security_descriptor_len,
                &mut security_descriptor,
            )
        };
        ensure_success(result, "BuildSecurityDescriptorW")?;

        Ok(Self {
            security_descriptor,
            blob: FWP_BYTE_BLOB {
                size: security_descriptor_len,
                data: security_descriptor as *mut u8,
            },
        })
    }
}

impl Drop for UserMatchCondition {
    fn drop(&mut self) {
        if !self.security_descriptor.is_null() {
            unsafe {
                LocalFree(self.security_descriptor as HLOCAL);
            }
        }
    }
}

/// Ensures the persistent Hive WFP provider exists.
fn ensure_provider(engine: HANDLE) -> Result<()> {
    let provider_name = to_wide(OsStr::new(PROVIDER_NAME));
    let provider_description = to_wide(OsStr::new(PROVIDER_DESCRIPTION));
    let provider = FWPM_PROVIDER0 {
        providerKey: PROVIDER_KEY,
        displayData: FWPM_DISPLAY_DATA0 {
            name: provider_name.as_ptr() as *mut _,
            description: provider_description.as_ptr() as *mut _,
        },
        flags: FWPM_PROVIDER_FLAG_PERSISTENT,
        providerData: empty_blob(),
        serviceName: null_mut(),
    };

    let result = unsafe { FwpmProviderAdd0(engine, &provider, null_mut()) };
    ensure_success_or(result, "FwpmProviderAdd0", &[FWP_E_ALREADY_EXISTS as u32])
}

/// Ensures the persistent Hive sublayer exists under the Hive provider.
fn ensure_sublayer(engine: HANDLE) -> Result<()> {
    let sublayer_name = to_wide(OsStr::new(SUBLAYER_NAME));
    let sublayer_description = to_wide(OsStr::new(SUBLAYER_DESCRIPTION));
    let provider_key = PROVIDER_KEY;
    let sublayer = FWPM_SUBLAYER0 {
        subLayerKey: SUBLAYER_KEY,
        displayData: FWPM_DISPLAY_DATA0 {
            name: sublayer_name.as_ptr() as *mut _,
            description: sublayer_description.as_ptr() as *mut _,
        },
        flags: FWPM_SUBLAYER_FLAG_PERSISTENT,
        providerKey: &provider_key as *const _ as *mut _,
        providerData: empty_blob(),
        weight: 0x8000,
    };

    let result = unsafe { FwpmSubLayerAdd0(engine, &sublayer, null_mut()) };
    ensure_success_or(result, "FwpmSubLayerAdd0", &[FWP_E_ALREADY_EXISTS as u32])
}

/// Adds one WFP filter (block or permit) from the static filter spec list.
fn add_filter(
    engine: HANDLE,
    spec: &FilterSpec,
    user_condition: &UserMatchCondition,
) -> Result<()> {
    let filter_name = to_wide(OsStr::new(spec.name));
    let filter_description = to_wide(OsStr::new(spec.description));

    // Owned condition-data holders. Their addresses are handed to WFP through
    // the FWPM_FILTER_CONDITION0 / FWPM_FILTER0 pointers below, so they MUST
    // outlive the FwpmFilterAdd0 call: keep them on this stack frame.
    let v4_loopback = FWP_V4_ADDR_AND_MASK {
        addr: 0x7F00_0000,
        mask: 0xFF00_0000,
    };
    let v6_loopback = FWP_V6_ADDR_AND_MASK {
        addr: [0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1],
        prefixLength: 128,
    };
    let (range_low, range_high) = spec
        .conditions
        .iter()
        .find_map(|condition| match condition {
            ConditionSpec::RemotePortRange(low, high) => Some((*low, *high)),
            _ => None,
        })
        .unwrap_or((0, 0));
    let port_range = make_port_range(range_low, range_high);
    // Manual weight value; referenced only through the `Some` arm below.
    let weight_val: u64 = spec.weight.unwrap_or(0);

    let mut filter_conditions = build_conditions(
        spec.conditions,
        user_condition,
        &v4_loopback,
        &v6_loopback,
        &port_range,
    );
    let provider_key = PROVIDER_KEY;
    let action_type = match spec.action {
        FilterAction::Block => FWP_ACTION_BLOCK,
        FilterAction::Permit => FWP_ACTION_PERMIT,
    };
    // `Some(w)` pins a manual FWP_UINT64 weight (the core loopback permit must
    // outrank the SID block within the sublayer); `None` leaves FWP_EMPTY so
    // the kernel auto-assigns. effectiveWeight is always empty (kernel-computed).
    let weight = match spec.weight {
        Some(_) => FWP_VALUE0 {
            r#type: FWP_UINT64,
            Anonymous: FWP_VALUE0_0 {
                uint64: &weight_val as *const u64 as *mut u64,
            },
        },
        None => empty_value(),
    };
    let filter = FWPM_FILTER0 {
        filterKey: spec.key,
        displayData: FWPM_DISPLAY_DATA0 {
            name: filter_name.as_ptr() as *mut _,
            description: filter_description.as_ptr() as *mut _,
        },
        flags: FWPM_FILTER_FLAG_PERSISTENT,
        providerKey: &provider_key as *const _ as *mut _,
        providerData: empty_blob(),
        layerKey: spec.layer_key,
        subLayerKey: SUBLAYER_KEY,
        weight,
        numFilterConditions: filter_conditions.len() as u32,
        filterCondition: filter_conditions.as_mut_ptr(),
        action: FWPM_ACTION0 {
            r#type: action_type,
            Anonymous: FWPM_ACTION0_0 {
                filterType: zero_guid(),
            },
        },
        Anonymous: FWPM_FILTER0_0 { rawContext: 0 },
        reserved: null_mut(),
        filterId: 0,
        effectiveWeight: empty_value(),
    };

    let mut filter_id = 0_u64;
    let result = unsafe { FwpmFilterAdd0(engine, &filter, null_mut(), &mut filter_id) };
    ensure_success(result, &format!("FwpmFilterAdd0({})", spec.name))
}

/// Builds an inclusive `low..=high` remote-port `FWP_RANGE0`. The range holds
/// inline `FWP_UINT16` values (no internal pointers), so a caller can keep it
/// on the stack and hand WFP a pointer to it.
fn make_port_range(low: u16, high: u16) -> FWP_RANGE0 {
    FWP_RANGE0 {
        valueLow: FWP_VALUE0 {
            r#type: FWP_UINT16,
            Anonymous: FWP_VALUE0_0 { uint16: low },
        },
        valueHigh: FWP_VALUE0 {
            r#type: FWP_UINT16,
            Anonymous: FWP_VALUE0_0 { uint16: high },
        },
    }
}

/// Converts our compact condition specs into WFP filter conditions. The
/// address / range holders are borrowed from the caller's stack (see
/// [`add_filter`]) so their pointers stay valid for the FwpmFilterAdd0 call.
fn build_conditions(
    specs: &[ConditionSpec],
    user_condition: &UserMatchCondition,
    v4_loopback: &FWP_V4_ADDR_AND_MASK,
    v6_loopback: &FWP_V6_ADDR_AND_MASK,
    port_range: &FWP_RANGE0,
) -> Vec<FWPM_FILTER_CONDITION0> {
    specs
        .iter()
        .map(|spec| match spec {
            ConditionSpec::User => FWPM_FILTER_CONDITION0 {
                fieldKey: FWPM_CONDITION_ALE_USER_ID,
                matchType: FWP_MATCH_EQUAL,
                conditionValue: FWP_CONDITION_VALUE0 {
                    r#type: FWP_SECURITY_DESCRIPTOR_TYPE,
                    Anonymous: FWP_CONDITION_VALUE0_0 {
                        sd: &user_condition.blob as *const _ as *mut _,
                    },
                },
            },
            ConditionSpec::Protocol(protocol) => FWPM_FILTER_CONDITION0 {
                fieldKey: FWPM_CONDITION_IP_PROTOCOL,
                matchType: FWP_MATCH_EQUAL,
                conditionValue: FWP_CONDITION_VALUE0 {
                    r#type: FWP_UINT8,
                    Anonymous: FWP_CONDITION_VALUE0_0 { uint8: *protocol },
                },
            },
            ConditionSpec::RemotePort(port) => FWPM_FILTER_CONDITION0 {
                fieldKey: FWPM_CONDITION_IP_REMOTE_PORT,
                matchType: FWP_MATCH_EQUAL,
                conditionValue: FWP_CONDITION_VALUE0 {
                    r#type: FWP_UINT16,
                    Anonymous: FWP_CONDITION_VALUE0_0 { uint16: *port },
                },
            },
            ConditionSpec::RemoteAddressLoopbackV4 => FWPM_FILTER_CONDITION0 {
                fieldKey: FWPM_CONDITION_IP_REMOTE_ADDRESS,
                matchType: FWP_MATCH_EQUAL,
                conditionValue: FWP_CONDITION_VALUE0 {
                    r#type: FWP_V4_ADDR_MASK,
                    Anonymous: FWP_CONDITION_VALUE0_0 {
                        v4AddrMask: v4_loopback as *const _ as *mut _,
                    },
                },
            },
            ConditionSpec::RemoteAddressLoopbackV6 => FWPM_FILTER_CONDITION0 {
                fieldKey: FWPM_CONDITION_IP_REMOTE_ADDRESS,
                matchType: FWP_MATCH_EQUAL,
                conditionValue: FWP_CONDITION_VALUE0 {
                    r#type: FWP_V6_ADDR_MASK,
                    Anonymous: FWP_CONDITION_VALUE0_0 {
                        v6AddrMask: v6_loopback as *const _ as *mut _,
                    },
                },
            },
            ConditionSpec::RemotePortRange(_, _) => FWPM_FILTER_CONDITION0 {
                fieldKey: FWPM_CONDITION_IP_REMOTE_PORT,
                matchType: FWP_MATCH_RANGE,
                conditionValue: FWP_CONDITION_VALUE0 {
                    r#type: FWP_RANGE_TYPE,
                    Anonymous: FWP_CONDITION_VALUE0_0 {
                        rangeValue: port_range as *const _ as *mut _,
                    },
                },
            },
        })
        .collect()
}

/// Deletes an old copy of a filter before re-adding it.
fn delete_filter_if_present(engine: HANDLE, key: &GUID) -> Result<()> {
    let result = unsafe { FwpmFilterDeleteByKey0(engine, key) };
    ensure_success_or(
        result,
        "FwpmFilterDeleteByKey0",
        &[FWP_E_FILTER_NOT_FOUND as u32, FWP_E_NOT_FOUND as u32],
    )
}

fn ensure_success(result: u32, operation: &str) -> Result<()> {
    ensure_success_or(result, operation, &[])
}

fn ensure_success_or(result: u32, operation: &str, allowed: &[u32]) -> Result<()> {
    if result == 0 || allowed.contains(&result) {
        Ok(())
    } else {
        Err(anyhow::anyhow!(
            "{operation} failed: {}",
            format_error_code(result)
        ))
    }
}

fn format_error_code(result: u32) -> String {
    format!("0x{result:08X}")
}

fn empty_blob() -> FWP_BYTE_BLOB {
    FWP_BYTE_BLOB {
        size: 0,
        data: null_mut(),
    }
}

fn empty_value() -> FWP_VALUE0 {
    FWP_VALUE0 {
        r#type: FWP_EMPTY,
        Anonymous: unsafe { zeroed() },
    }
}

fn zero_guid() -> GUID {
    GUID::from_u128(0)
}

#[cfg(test)]
mod tests {
    use super::FILTER_SPECS;
    use pretty_assertions::assert_eq;
    use std::collections::BTreeSet;

    #[test]
    fn filter_keys_are_unique() {
        let keys = FILTER_SPECS
            .iter()
            .map(|spec| {
                (
                    spec.key.data1,
                    spec.key.data2,
                    spec.key.data3,
                    spec.key.data4,
                )
            })
            .collect::<BTreeSet<_>>();
        assert_eq!(keys.len(), FILTER_SPECS.len());
    }

    #[test]
    fn filter_names_are_unique() {
        let names = FILTER_SPECS
            .iter()
            .map(|spec| spec.name)
            .collect::<BTreeSet<_>>();
        assert_eq!(names.len(), FILTER_SPECS.len());
    }

    #[test]
    fn core_has_both_permit_and_block_filters() {
        use super::FilterAction;
        // Runtime insurance that the two-layer core survives an edit: the
        // permit-above-block WEIGHT ordering itself is guaranteed at compile
        // time by the `const _: () = assert!(W_LOOPBACK > W_BLOCK)` in
        // filter_specs, so it is not re-asserted here (clippy rejects a
        // runtime assert over two consts).
        let has_permit = FILTER_SPECS
            .iter()
            .any(|spec| matches!(spec.action, FilterAction::Permit));
        let has_block = FILTER_SPECS
            .iter()
            .any(|spec| matches!(spec.action, FilterAction::Block));
        assert!(has_permit, "expected at least one Permit core filter");
        assert!(has_block, "expected at least one Block filter");
    }
}
