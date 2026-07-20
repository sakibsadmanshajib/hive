// Local deviation from upstream (`pub(crate) mod` there): `pub` here so lib.rs's
// `pub use elevated::X;` re-exports (see lib.rs) do not leak a lower-visibility
// module through a higher-visibility re-export. See lib.rs's comment for why
// this crate uses `pub` throughout rather than upstream's curated `pub(crate)`
// surface.
pub mod ipc_framed;
pub mod runner_client;
pub mod runner_pipe;
