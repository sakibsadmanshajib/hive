#!/usr/bin/env bash
# test-pull-box-backups.sh -- proves the off-box pull records its freshness
# marker ONLY after a verified pull (issue #1491).
#
# The marker is what a scheduled lane reads to decide whether the off-box copy
# of the demo box's backups has gone stale. If it were written before or
# independently of verification, it would be one more surface asserting a
# state the system does not have, which is exactly the defect the marker
# exists to fix. So the ordering is not a detail, it is the whole property,
# and it gets a test.
#
# Four scenarios, all against fake `rsync` and `gh` binaries on PATH, so no
# network, no ssh, no box and no GitHub are touched:
#   1. a complete, correctly checksummed day  -> exit 0 and the marker IS
#      written, carrying that day as newest_day;
#   2. a day whose checksums do not match     -> exit 1 and gh is NEVER
#      invoked, leaving a pre-existing marker exactly as it was;
#   3. a day missing SHA256SUMS entirely      -> exit 1 and gh is NEVER
#      invoked (the incomplete-set path is a different branch from the
#      checksum-mismatch path, so it is proved separately);
#   4. gh failing while everything else is fine -> exit 1, because an
#      unrecorded pull is invisible to the scheduled check and reporting
#      success over it would restore the silence.
#
# Run: bash scripts/test-pull-box-backups.sh
set -euo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SCRIPT="$HERE/pull-box-backups.sh"

work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

fake_bin="$work/bin"
mkdir -p "$fake_bin"

# rsync is a no-op: every fixture is pre-placed in DEST, so there is nothing
# to transfer and nothing about the transfer under test here.
cat > "$fake_bin/rsync" <<'RSYNC_EOF'
#!/usr/bin/env bash
exit 0
RSYNC_EOF
chmod +x "$fake_bin/rsync"

# gh appends its whole argv to $GH_CALL_LOG so a test can assert both that it
# was called and what with, and exits with $FAKE_GH_EXIT.
cat > "$fake_bin/gh" <<'GH_EOF'
#!/usr/bin/env bash
printf '%s\n' "$*" >> "$GH_CALL_LOG"
exit "${FAKE_GH_EXIT:-0}"
GH_EOF
chmod +x "$fake_bin/gh"

failures=0
pass() { printf 'ok   %s\n' "$1"; }
fail() { printf 'FAIL %s: %s\n' "$1" "$2" >&2; failures=$((failures + 1)); }

# Builds one day directory shaped like the box publishes it: four encrypted
# artifacts, a MANIFEST.txt and a SHA256SUMS over exactly those four.
make_day() {
  local dest="$1" day="$2" corrupt="${3:-no}"
  local dir="$dest/daily/$day"
  mkdir -p "$dir"
  local f
  for f in db.pgdump webui.db uploads.tgz storage.tgz; do
    printf 'ciphertext for %s on %s\n' "$f" "$day" > "$dir/$f.enc"
  done
  ( cd "$dir" && ls | grep '\.enc$' | sort | xargs sha256sum > SHA256SUMS )
  printf 'created %s\n' "$day" > "$dir/MANIFEST.txt"
  if [[ "$corrupt" == "corrupt" ]]; then
    # Rewrite one artifact AFTER the checksums were taken: the exact shape of
    # a truncated or tampered transfer.
    printf 'tampered\n' > "$dir/db.pgdump.enc"
  fi
}

run_script() {
  local dest="$1" log="$2" gh_exit="${3:-0}"
  set +e
  PATH="$fake_bin:$PATH" \
  BOX=fake-box \
  DEST="$dest" \
  MARKER_REPO=fake/repo \
  MARKER_VAR=TEST_MARKER \
  GH_CALL_LOG="$log" \
  FAKE_GH_EXIT="$gh_exit" \
    bash "$SCRIPT" > "$dest/stdout.txt" 2> "$dest/stderr.txt"
  local status=$?
  set -e
  return "$status"
}

# --- 1. verified pull records the marker -----------------------------------
name='a verified pull records the marker with the newest day'
dest="$work/case-verified"
log="$dest/gh-calls.txt"
mkdir -p "$dest"
make_day "$dest" 2026-08-27
make_day "$dest" 2026-08-29
status=0
run_script "$dest" "$log" || status=$?
if (( status == 0 )); then
  if [[ ! -s "$log" ]]; then
    fail "$name" "gh was never called, so nothing recorded the pull"
  elif ! grep -q 'variable set TEST_MARKER' "$log"; then
    fail "$name" "gh was called but not to set the marker: $(cat "$log")"
  elif ! grep -q 'newest_day=2026-08-29' "$log"; then
    fail "$name" "the marker does not name the newest day: $(cat "$log")"
  elif ! grep -q 'pulled_at=[0-9]\{4\}-[0-9]\{2\}-[0-9]\{2\}T[0-9:]*Z' "$log"; then
    fail "$name" "the marker carries no parseable pulled_at: $(cat "$log")"
  else
    pass "$name"
  fi
else
  fail "$name" "expected exit 0, got $status. stderr: $(cat "$dest/stderr.txt")"
fi

# --- 2. checksum mismatch leaves the previous marker alone -----------------
name='a checksum mismatch FAILS and never touches the marker'
dest="$work/case-corrupt"
log="$dest/gh-calls.txt"
mkdir -p "$dest"
make_day "$dest" 2026-08-29 corrupt
if run_script "$dest" "$log"; then
  fail "$name" "expected a non-zero exit, got 0"
elif [[ -s "$log" ]]; then
  fail "$name" "gh was invoked on a failed verification: $(cat "$log")"
elif ! grep -q 'marker was NOT updated' "$dest/stderr.txt"; then
  fail "$name" "the failure does not say the marker was left alone: $(cat "$dest/stderr.txt")"
else
  pass "$name"
fi

# --- 3. an incomplete set is a different branch, prove it too --------------
name='a day missing SHA256SUMS FAILS and never touches the marker'
dest="$work/case-incomplete"
log="$dest/gh-calls.txt"
mkdir -p "$dest"
make_day "$dest" 2026-08-29
rm -f "$dest/daily/2026-08-29/SHA256SUMS"
if run_script "$dest" "$log"; then
  fail "$name" "expected a non-zero exit, got 0"
elif [[ -s "$log" ]]; then
  fail "$name" "gh was invoked on an incomplete set: $(cat "$log")"
else
  pass "$name"
fi

# --- 3a. a short artifact count FAILS --------------------------------------
name='a day holding three of the four artifacts FAILS'
dest="$work/case-three-artifacts"
log="$dest/gh-calls.txt"
mkdir -p "$dest"
make_day "$dest" 2026-08-29
rm -f "$dest/daily/2026-08-29/storage.tgz.enc"
status=0
run_script "$dest" "$log" || status=$?
if (( status == 0 )); then
  fail "$name" "expected a non-zero exit, got 0"
elif [[ -s "$log" ]]; then
  fail "$name" "gh was invoked over a partial set: $(cat "$log")"
else
  pass "$name"
fi

# --- 3b. a short SHA256SUMS is a vacuous pass, not a pass -------------------
name='a SHA256SUMS listing fewer than four artifacts FAILS'
dest="$work/case-short-sums"
log="$dest/gh-calls.txt"
mkdir -p "$dest"
make_day "$dest" 2026-08-29
# Every artifact is present and intact; only the checksum file is short. Without
# the line-count guard `sha256sum -c` verifies the two lines it was given, says
# nothing at all about the other two, and exits 0.
head -2 "$dest/daily/2026-08-29/SHA256SUMS" > "$dest/sums.tmp"
mv "$dest/sums.tmp" "$dest/daily/2026-08-29/SHA256SUMS"
status=0
run_script "$dest" "$log" || status=$?
if (( status == 0 )); then
  fail "$name" "expected a non-zero exit, got 0"
elif [[ -s "$log" ]]; then
  fail "$name" "gh was invoked over a half-verified set: $(cat "$log")"
else
  pass "$name"
fi

# --- 4. a failed marker write is loud --------------------------------------
name='a failed marker write FAILS the run rather than reporting success'
dest="$work/case-gh-down"
log="$dest/gh-calls.txt"
mkdir -p "$dest"
make_day "$dest" 2026-08-29
if run_script "$dest" "$log" 1; then
  fail "$name" "expected a non-zero exit, got 0"
elif ! grep -q 'freshness marker' "$dest/stderr.txt"; then
  fail "$name" "the failure does not name the marker: $(cat "$dest/stderr.txt")"
else
  pass "$name"
fi

if (( failures > 0 )); then
  printf '\n%d check(s) failed\n' "$failures" >&2
  exit 1
fi
printf '\nall pull-box-backups marker checks passed\n'
