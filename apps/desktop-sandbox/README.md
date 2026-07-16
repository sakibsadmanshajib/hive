# desktop-sandbox

Native OS sandbox backends for running an agent task under the Hive desktop
app, one per platform. `hive-desktop-sandbox::launch` is the only stable
entry point the desktop shell (and, later, the OpenHands `workspace_factory`
plugin point) needs to call. See `VENDORING.md` for what is vendored from
`openai/codex` (Apache-2.0) versus newly authored, and for open risks.

- `codex-bwrap/` -- vendored: builds the bundled `bwrap` binary from
  `vendor/bubblewrap/` (this crate's sibling directory).
- `codex-process-hardening/` -- vendored: pre-exec hardening applied before
  the sandboxed process's confinement is set up.
- `hive-desktop-sandbox/` -- Hive-proprietary: policy model plus the Linux
  (bubblewrap + Landlock + seccomp-BPF) and Windows (restricted token +
  directory ACL + Job Object) backends.

Linux only, `x86_64`. No macOS backend (deferred, blueprint Step 5.6).

## Build and test

```
cargo fmt --check
cargo clippy --all-targets -- -D warnings
cargo test
```

`codex-bwrap`'s `build.rs` compiles vendored bubblewrap and needs `libcap`
dev headers (`apt-get install libcap-dev pkg-config` on Debian/Ubuntu). Set
`CODEX_SKIP_BWRAP_BUILD=1` to skip that compilation step (e.g. quick
`cargo check` without libcap installed); the resulting binary will panic if
actually run.

Cross-check the Windows backend type-checks without a Windows host:

```
rustup target add x86_64-pc-windows-gnu
cargo clippy --target x86_64-pc-windows-gnu --all-targets -- -D warnings
```

This does not replace a real MSVC build and a behavioral run in the lab; see
`VENDORING.md` "Open risks".

## Ubuntu unprivileged userns

Stock Ubuntu 24.04+ sets `kernel.apparmor_restrict_unprivileged_userns=1`,
which blocks bubblewrap's `--unshare-user` for any unconfined or
non-`userns`-granted process. Install the bundled AppArmor profile so the
packaged `bwrap` binary can still create its own user namespace:

```
sudo install -m 0644 hive-desktop-sandbox/assets/apparmor/hive-bwrap-userns \
  /etc/apparmor.d/hive-bwrap-userns
sudo apparmor_parser -r /etc/apparmor.d/hive-bwrap-userns
sudo aa-status | grep hive-bwrap-userns
```

Adjust the `profile` path in that file to match wherever the desktop app's
packaging step actually places the `bwrap` binary.
