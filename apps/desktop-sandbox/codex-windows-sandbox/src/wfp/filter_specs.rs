// Vendored from openai/codex codex-rs/windows-sandbox-rs/src/wfp/filter_specs.rs
// at commit a47c661ea9e226fe65e46cf9dbc5c5ed75c2c762 (Apache-2.0). Deviations
// from upstream (see ../../VENDORING.md): every GUID is a freshly minted
// Hive-owned identity (never Codex's, so a Hive install never shares a WFP
// namespace with a Codex install on the same box), and every filter `name` is
// renamed `codex_wfp_*` -> `hive_wfp_*`.
//
// The filter set now has two parts (Integration B2 activation), forming a
// complete two-layer WFP fence (D-011):
//   1. the srt-shape CORE (added by Hive): a loopback PERMIT above a
//      sandbox-SID BLOCK-all, at the ALE_AUTH_CONNECT layers, so the account
//      may reach only the loopback egress proxy port range and nothing else;
//   2. the Codex per-protocol blocks (ALE_USER_ID + per-protocol block at the
//      ALE_AUTH_CONNECT / ALE_RESOURCE_ASSIGNMENT layers), unchanged in shape.
// The core is ordered FIRST (higher weight) so the loopback permit wins over
// the SID block within the Hive sublayer.
use windows_sys::Win32::NetworkManagement::WindowsFilteringPlatform::FWPM_LAYER_ALE_AUTH_CONNECT_V4;
use windows_sys::Win32::NetworkManagement::WindowsFilteringPlatform::FWPM_LAYER_ALE_AUTH_CONNECT_V6;
use windows_sys::Win32::NetworkManagement::WindowsFilteringPlatform::FWPM_LAYER_ALE_RESOURCE_ASSIGNMENT_V4;
use windows_sys::Win32::NetworkManagement::WindowsFilteringPlatform::FWPM_LAYER_ALE_RESOURCE_ASSIGNMENT_V6;
use windows_sys::Win32::Networking::WinSock::IPPROTO_ICMP;
use windows_sys::Win32::Networking::WinSock::IPPROTO_ICMPV6;
use windows_sys::core::GUID;

/// Whether a filter permits or blocks the traffic it matches.
#[derive(Clone, Copy)]
pub(super) enum FilterAction {
    Block,
    Permit,
}

#[derive(Clone, Copy)]
pub(super) enum ConditionSpec {
    User,
    Protocol(u8),
    RemotePort(u16),
    /// Remote address equals the IPv4 loopback range (`127.0.0.0/8`).
    RemoteAddressLoopbackV4,
    /// Remote address equals the IPv6 loopback (`::1/128`).
    RemoteAddressLoopbackV6,
    /// Remote port falls inside the inclusive range `low..=high`.
    RemotePortRange(u16, u16),
}

#[derive(Clone, Copy)]
pub(super) struct FilterSpec {
    pub(super) key: GUID,
    pub(super) name: &'static str,
    pub(super) description: &'static str,
    pub(super) layer_key: GUID,
    pub(super) conditions: &'static [ConditionSpec],
    pub(super) action: FilterAction,
    /// Explicit filter weight. `None` lets the kernel auto-assign (FWP_EMPTY);
    /// `Some(w)` pins a manual FWP_UINT64 weight so the core loopback permit
    /// can be ordered above the SID block within the sublayer.
    pub(super) weight: Option<u64>,
}

/// Weight of the loopback PERMIT filters. MUST exceed [`W_BLOCK`] so that,
/// within the Hive sublayer, a loopback-to-proxy connect is permitted even
/// though the SID block-all would otherwise deny it (the srt-shape core).
pub(super) const W_LOOPBACK: u64 = 0x0F80_0000_0000_0000;
/// Weight of the sandbox-SID BLOCK-all filters.
pub(super) const W_BLOCK: u64 = 0x0F40_0000_0000_0000;
const _: () = assert!(W_LOOPBACK > W_BLOCK);

/// Low bound of the loopback egress-proxy port range.
///
/// MUST match `hive_desktop_sandbox::wfp_ports::PROXY_PORT_RANGE` (different
/// crate, cannot import; drift asserted by wfp_ports test).
pub(super) const PROXY_PORT_LOW: u16 = 62080;
/// High bound of the loopback egress-proxy port range. See [`PROXY_PORT_LOW`].
pub(super) const PROXY_PORT_HIGH: u16 = 62089;

pub(super) const FILTER_SPECS: &[FilterSpec] = &[
    // ---- srt-shape CORE (Hive): loopback permit above SID block-all --------
    FilterSpec {
        key: GUID::from_u128(0x0b7f4d21_c3a8_4e56_9f10_2a5c7e881b04),
        name: "hive_wfp_loopback_permit_v4",
        description: "Permit sandbox loopback egress-proxy connect v4",
        layer_key: FWPM_LAYER_ALE_AUTH_CONNECT_V4,
        conditions: &[
            ConditionSpec::RemoteAddressLoopbackV4,
            ConditionSpec::RemotePortRange(PROXY_PORT_LOW, PROXY_PORT_HIGH),
        ],
        action: FilterAction::Permit,
        weight: Some(W_LOOPBACK),
    },
    FilterSpec {
        key: GUID::from_u128(0x0b7f4d22_c3a8_4e56_9f10_2a5c7e881b05),
        name: "hive_wfp_loopback_permit_v6",
        description: "Permit sandbox loopback egress-proxy connect v6",
        layer_key: FWPM_LAYER_ALE_AUTH_CONNECT_V6,
        conditions: &[
            ConditionSpec::RemoteAddressLoopbackV6,
            ConditionSpec::RemotePortRange(PROXY_PORT_LOW, PROXY_PORT_HIGH),
        ],
        action: FilterAction::Permit,
        weight: Some(W_LOOPBACK),
    },
    FilterSpec {
        key: GUID::from_u128(0x0b7f4d23_c3a8_4e56_9f10_2a5c7e881b06),
        name: "hive_wfp_block_user_v4",
        description: "Block sandbox-account outbound connect v4 (all)",
        layer_key: FWPM_LAYER_ALE_AUTH_CONNECT_V4,
        conditions: &[ConditionSpec::User],
        action: FilterAction::Block,
        weight: Some(W_BLOCK),
    },
    FilterSpec {
        key: GUID::from_u128(0x0b7f4d24_c3a8_4e56_9f10_2a5c7e881b07),
        name: "hive_wfp_block_user_v6",
        description: "Block sandbox-account outbound connect v6 (all)",
        layer_key: FWPM_LAYER_ALE_AUTH_CONNECT_V6,
        conditions: &[ConditionSpec::User],
        action: FilterAction::Block,
        weight: Some(W_BLOCK),
    },
    // ---- Codex per-protocol blocks (shape unchanged) -----------------------
    FilterSpec {
        key: GUID::from_u128(0x7c8da4d6_ec2f_4225_8a11_3252affda1ef),
        name: "hive_wfp_icmp_connect_v4",
        description: "Block sandbox-account ICMP connect v4",
        layer_key: FWPM_LAYER_ALE_AUTH_CONNECT_V4,
        conditions: &[
            ConditionSpec::User,
            ConditionSpec::Protocol(IPPROTO_ICMP as u8),
        ],
        action: FilterAction::Block,
        weight: None,
    },
    FilterSpec {
        key: GUID::from_u128(0x10c896cc_7772_4c25_abf5_ed33d2fd61c3),
        name: "hive_wfp_icmp_connect_v6",
        description: "Block sandbox-account ICMP connect v6",
        layer_key: FWPM_LAYER_ALE_AUTH_CONNECT_V6,
        conditions: &[
            ConditionSpec::User,
            ConditionSpec::Protocol(IPPROTO_ICMPV6 as u8),
        ],
        action: FilterAction::Block,
        weight: None,
    },
    FilterSpec {
        key: GUID::from_u128(0x1513bc34_4515_4f60_83a0_e928d994b14c),
        name: "hive_wfp_icmp_assign_v4",
        description: "Block sandbox-account ICMP resource assignment v4",
        layer_key: FWPM_LAYER_ALE_RESOURCE_ASSIGNMENT_V4,
        conditions: &[
            ConditionSpec::User,
            ConditionSpec::Protocol(IPPROTO_ICMP as u8),
        ],
        action: FilterAction::Block,
        weight: None,
    },
    FilterSpec {
        key: GUID::from_u128(0x141ff07b_9e71_4099_8be9_b4c9decf8fc0),
        name: "hive_wfp_icmp_assign_v6",
        description: "Block sandbox-account ICMP resource assignment v6",
        layer_key: FWPM_LAYER_ALE_RESOURCE_ASSIGNMENT_V6,
        conditions: &[
            ConditionSpec::User,
            ConditionSpec::Protocol(IPPROTO_ICMPV6 as u8),
        ],
        action: FilterAction::Block,
        weight: None,
    },
    // NAME_RESOLUTION_CACHE filters are intentionally omitted because ordinary
    // static filter shapes returned FWP_E_OUT_OF_BOUNDS during upstream
    // validation.
    FilterSpec {
        key: GUID::from_u128(0x4d93439a_6900_47e6_b42d_b4509696cf15),
        name: "hive_wfp_dns_53_v4",
        description: "Block sandbox-account DNS TCP or UDP port 53 v4",
        layer_key: FWPM_LAYER_ALE_AUTH_CONNECT_V4,
        conditions: &[ConditionSpec::User, ConditionSpec::RemotePort(53)],
        action: FilterAction::Block,
        weight: None,
    },
    FilterSpec {
        key: GUID::from_u128(0xa069500a_60f9_4e22_9b6a_bf94916e4aaf),
        name: "hive_wfp_dns_53_v6",
        description: "Block sandbox-account DNS TCP or UDP port 53 v6",
        layer_key: FWPM_LAYER_ALE_AUTH_CONNECT_V6,
        conditions: &[ConditionSpec::User, ConditionSpec::RemotePort(53)],
        action: FilterAction::Block,
        weight: None,
    },
    FilterSpec {
        key: GUID::from_u128(0xe9edcf72_649f_4d46_8477_52e760a48a1a),
        name: "hive_wfp_dns_853_v4",
        description: "Block sandbox-account DNS-over-TLS port 853 v4",
        layer_key: FWPM_LAYER_ALE_AUTH_CONNECT_V4,
        conditions: &[ConditionSpec::User, ConditionSpec::RemotePort(853)],
        action: FilterAction::Block,
        weight: None,
    },
    FilterSpec {
        key: GUID::from_u128(0x78613308_bb1b_46d9_acaa_6340b1f1f550),
        name: "hive_wfp_dns_853_v6",
        description: "Block sandbox-account DNS-over-TLS port 853 v6",
        layer_key: FWPM_LAYER_ALE_AUTH_CONNECT_V6,
        conditions: &[ConditionSpec::User, ConditionSpec::RemotePort(853)],
        action: FilterAction::Block,
        weight: None,
    },
    FilterSpec {
        key: GUID::from_u128(0x76805d3f_5ab5_4986_9687_ab5a17604b16),
        name: "hive_wfp_smb_445_v4",
        description: "Block sandbox-account SMB port 445 v4",
        layer_key: FWPM_LAYER_ALE_AUTH_CONNECT_V4,
        conditions: &[ConditionSpec::User, ConditionSpec::RemotePort(445)],
        action: FilterAction::Block,
        weight: None,
    },
    FilterSpec {
        key: GUID::from_u128(0xf8f6080d_c477_471a_9104_8007d28ed672),
        name: "hive_wfp_smb_445_v6",
        description: "Block sandbox-account SMB port 445 v6",
        layer_key: FWPM_LAYER_ALE_AUTH_CONNECT_V6,
        conditions: &[ConditionSpec::User, ConditionSpec::RemotePort(445)],
        action: FilterAction::Block,
        weight: None,
    },
    FilterSpec {
        key: GUID::from_u128(0x86cdc507_9476_45c3_8997_6188664eb02e),
        name: "hive_wfp_smb_139_v4",
        description: "Block sandbox-account SMB port 139 v4",
        layer_key: FWPM_LAYER_ALE_AUTH_CONNECT_V4,
        conditions: &[ConditionSpec::User, ConditionSpec::RemotePort(139)],
        action: FilterAction::Block,
        weight: None,
    },
    FilterSpec {
        key: GUID::from_u128(0xbd7f5ac9_39dd_4a45_9370_3d55e3831ce6),
        name: "hive_wfp_smb_139_v6",
        description: "Block sandbox-account SMB port 139 v6",
        layer_key: FWPM_LAYER_ALE_AUTH_CONNECT_V6,
        conditions: &[ConditionSpec::User, ConditionSpec::RemotePort(139)],
        action: FilterAction::Block,
        weight: None,
    },
];
