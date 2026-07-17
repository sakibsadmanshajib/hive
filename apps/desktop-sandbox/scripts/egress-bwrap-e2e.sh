#!/usr/bin/env bash
# Live end-to-end proof that the Linux desktop egress allowlist actually
# blocks disallowed outbound traffic inside a real bubblewrap sandbox, and
# lets allowlisted traffic through (issue #342 item 6; firewall shipped in
# #341). This exercises the shipped enforcement components verbatim:
#
#   * codex-bwrap's `bwrap` binary with the same namespace flags
#     `linux::build_bwrap_argv` uses (crucially `--unshare-net`),
#   * the real `AllowlistProxy` (src/egress_proxy.rs), started by the
#     `egress-proxy-harness` example on a Unix socket,
#   * the real `hive-egress-shim` binary, which bridges that bind-mounted
#     socket to a loopback HTTP proxy inside the sandbox.
#
# Three assertions, all evaluated from inside the sandbox:
#   1. ALLOWED   host reached through the proxy  -> HTTP 200.
#   2. DISALLOWED host reached through the proxy  -> refused with 403 at the
#      allowlist boundary (never dialed out).
#   3. DIRECT    connection bypassing the proxy   -> refused, because
#      `--unshare-net` leaves the sandbox with no route but the bound socket.
#
# Determinism: the "internet" is two local loopback destinations backed by one
# throwaway HTTP server. No external hosts, no DNS, no TLS. The allowed and
# disallowed targets differ only by IP (127.0.0.2 vs 127.0.0.3) so that the
# ONLY thing separating a 200 from a 403 is the allowlist decision itself, not
# reachability (both answer on the host). 127.0.0.1 is avoided on purpose:
# the shim exports NO_PROXY=127.0.0.1,localhost, which would make the client
# bypass the proxy for that address.
#
# Env:
#   HIVE_BWRAP_PATH           use this bwrap (else build codex-bwrap, else
#                             fall back to a system bwrap).
#   HIVE_EGRESS_E2E_REQUIRE=1 treat a missing prerequisite (no usable
#                             unprivileged userns, no http client) as a hard
#                             failure instead of a skip. CI sets this.
set -euo pipefail

log()  { printf '[egress-e2e] %s\n' "$*"; }
fail() { printf '[egress-e2e] FAIL: %s\n' "$*" >&2; exit 1; }

REQUIRE="${HIVE_EGRESS_E2E_REQUIRE:-0}"
skip_or_fail() {
  if [ "$REQUIRE" = "1" ]; then fail "$1"; fi
  log "SKIP: $1"
  exit 0
}

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SANDBOX_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"          # apps/desktop-sandbox
MANIFEST="$SANDBOX_DIR/Cargo.toml"
TARGET_DIR="$SANDBOX_DIR/target"

HTTP_CLIENT="$(command -v curl || true)"
[ -n "$HTTP_CLIENT" ] || skip_or_fail "curl not found"
command -v python3 >/dev/null 2>&1 || skip_or_fail "python3 not found"
command -v cargo   >/dev/null 2>&1 || fail "cargo not found"

# --- build the shipped in-sandbox components (shim + proxy harness) ---------
log "building hive-egress-shim + egress-proxy-harness"
cargo build --manifest-path "$MANIFEST" -p hive-desktop-sandbox \
  --bin hive-egress-shim --example egress-proxy-harness
SHIM="$TARGET_DIR/debug/hive-egress-shim"
PROXY_HARNESS="$TARGET_DIR/debug/examples/egress-proxy-harness"
[ -x "$SHIM" ]          || fail "shim not built at $SHIM"
[ -x "$PROXY_HARNESS" ] || fail "proxy harness not built at $PROXY_HARNESS"

# --- resolve bwrap: env override, then codex-bwrap, then system -------------
BWRAP="${HIVE_BWRAP_PATH:-}"
if [ -z "$BWRAP" ]; then
  if cargo build --manifest-path "$MANIFEST" -p codex-bwrap --bin bwrap >/dev/null 2>&1 \
     && [ -x "$TARGET_DIR/debug/bwrap" ]; then
    BWRAP="$TARGET_DIR/debug/bwrap"
    log "using codex-bwrap build: $BWRAP"
  elif command -v bwrap >/dev/null 2>&1; then
    BWRAP="$(command -v bwrap)"
    log "using system bwrap: $BWRAP (codex-bwrap build unavailable; missing libcap/pkg-config?)"
  else
    skip_or_fail "no bwrap available (set HIVE_BWRAP_PATH or install bubblewrap)"
  fi
else
  log "using HIVE_BWRAP_PATH: $BWRAP"
fi
[ -x "$BWRAP" ] || fail "bwrap not executable: $BWRAP"

# --- temp workspace + cleanup ----------------------------------------------
WORK="$(mktemp -d)"
SOCK_DIR="$WORK/sock"
mkdir -p "$SOCK_DIR"
SOCK="$SOCK_DIR/egress.sock"
DOCROOT="$WORK/docroot"
mkdir -p "$DOCROOT"
printf 'ok' > "$DOCROOT/index.html"

ORIGIN_PID=""
PROXY_PID=""
cleanup() {
  [ -n "$PROXY_PID" ]  && kill "$PROXY_PID"  2>/dev/null || true
  [ -n "$ORIGIN_PID" ] && kill "$ORIGIN_PID" 2>/dev/null || true
  rm -rf "$WORK"
}
trap cleanup EXIT

# --- unprivileged-userns + unshare-net smoke test ---------------------------
if ! "$BWRAP" --unshare-user --unshare-net --ro-bind / / --proc /proc --dev /dev \
     -- /bin/true >/dev/null 2>&1; then
  skip_or_fail "bwrap cannot create an unprivileged user+network namespace \
(on Ubuntu try: sudo sysctl -w kernel.apparmor_restrict_unprivileged_userns=0)"
fi

# --- one throwaway HTTP origin, reachable on all loopback IPs ---------------
ORIGIN_PORT="$(python3 -c 'import socket;s=socket.socket();s.bind(("127.0.0.1",0));print(s.getsockname()[1]);s.close()')"
( cd "$DOCROOT" && exec python3 -m http.server "$ORIGIN_PORT" --bind 0.0.0.0 ) >"$WORK/origin.log" 2>&1 &
ORIGIN_PID=$!
for _ in $(seq 1 50); do
  if python3 -c 'import socket,sys; s=socket.socket(); s.settimeout(0.2);
sys.exit(0 if s.connect_ex(("127.0.0.1",int(sys.argv[1])))==0 else 1)' "$ORIGIN_PORT" 2>/dev/null; then
    break
  fi
  sleep 0.1
done
python3 -c 'import socket,sys; s=socket.socket(); s.settimeout(1);
sys.exit(0 if s.connect_ex(("127.0.0.2",int(sys.argv[1])))==0 else 1)' "$ORIGIN_PORT" \
  || fail "origin server did not come up on 127.0.0.2:$ORIGIN_PORT"
log "origin up on 127.0.0.2:$ORIGIN_PORT and 127.0.0.3:$ORIGIN_PORT (host); control: reachable on host"

# --- start the real AllowlistProxy: allow 127.0.0.2 only --------------------
"$PROXY_HARNESS" "$SOCK" 127.0.0.2 >"$WORK/proxy.log" 2>&1 &
PROXY_PID=$!
for _ in $(seq 1 50); do [ -S "$SOCK" ] && break; sleep 0.1; done
[ -S "$SOCK" ] || fail "allowlist proxy socket never appeared at $SOCK"
log "allowlist proxy listening on $SOCK (allow=127.0.0.2)"

# --- run a command inside a real bwrap sandbox ------------------------------
# Namespace flags mirror linux::build_bwrap_argv (all unshares incl. --net);
# the egress socket dir is bind-mounted read-write, everything else read-only.
# ponytail: `--ro-bind / /` is a test-harness convenience so curl + libs are
# present; the shipped policy binds specific roots. Neither changes the egress
# property under test (netns isolation + the single bound socket).
run_in_sandbox() {
  "$BWRAP" \
    --unshare-user --unshare-pid --unshare-ipc --unshare-uts --unshare-net \
    --die-with-parent \
    --ro-bind / / \
    --proc /proc --dev /dev \
    --bind "$SOCK_DIR" "$SOCK_DIR" \
    --chdir / \
    -- "$@"
}

# curl through the shim reads the proxy from $HTTPS_PROXY (set inside the
# sandbox by the shim) and --proxytunnel forces a CONNECT even for http://.
proxied_probe() { # $1 = target ip
  printf 'curl --proxytunnel -sS -m 8 -o /dev/null -w "%%{http_code}" -x "$HTTPS_PROXY" http://%s:%s/' \
    "$1" "$ORIGIN_PORT"
}
direct_probe() { # $1 = target ip  (no proxy: proves netns isolation)
  printf 'curl -sS -m 5 -o /dev/null -w "%%{http_code}" http://%s:%s/' \
    "$1" "$ORIGIN_PORT"
}

log "test 1: ALLOWED (127.0.0.2) through shim+proxy -> expect HTTP 200"
set +e
allowed_out="$(run_in_sandbox "$SHIM" "$SOCK" -- /bin/sh -c "$(proxied_probe 127.0.0.2)" 2>"$WORK/allowed.err")"
allowed_rc=$?
set -e
[ "$allowed_rc" -eq 0 ] || { cat "$WORK/allowed.err" >&2; fail "allowed probe exited $allowed_rc (expected 0)"; }
[ "$allowed_out" = "200" ] || fail "allowed probe returned '$allowed_out' (expected 200)"
log "  -> allowed reached origin: HTTP $allowed_out"

log "test 2: DISALLOWED (127.0.0.3) through shim+proxy -> expect 403 block"
set +e
blocked_all="$(run_in_sandbox "$SHIM" "$SOCK" -- /bin/sh -c "$(proxied_probe 127.0.0.3)" 2>&1)"
blocked_rc=$?
set -e
[ "$blocked_rc" -ne 0 ] || fail "disallowed probe unexpectedly succeeded: '$blocked_all'"
printf '%s' "$blocked_all" | grep -q '403' \
  || fail "disallowed probe blocked but no 403 seen (got: '$blocked_all')"
log "  -> disallowed refused at allowlist: rc=$blocked_rc, 403 observed"

log "test 3: DIRECT (127.0.0.2, no proxy) inside netns -> expect refused"
set +e
direct_all="$(run_in_sandbox /bin/sh -c "$(direct_probe 127.0.0.2)" 2>&1)"
direct_rc=$?
set -e
[ "$direct_rc" -ne 0 ] || fail "direct probe reached origin inside netns (unshare-net not enforced!): '$direct_all'"
log "  -> direct egress dead inside netns: rc=$direct_rc"

log "PASS: egress allowlist enforced inside a real bwrap sandbox"
log "SUMMARY allowed=$allowed_out(rc=$allowed_rc) disallowed_rc=$blocked_rc(403) direct_rc=$direct_rc bwrap=$BWRAP"
