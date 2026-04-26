#!/usr/bin/env bash
# verify-requirements-matrix.sh
#
# Parses .planning/REQUIREMENTS.md, extracts every Evidence-column markdown
# link of the form `[label](phases/.../evidence/*.md)`, asserts the file
# exists relative to .planning/, and asserts each evidence file carries the
# required frontmatter keys.
#
# Rows whose Evidence column reads "Phase NN (planned)" or
# "Phase NN (archive ...)" are intentionally skipped — those are pending
# markers and pre-Phase-11 archived rows respectively.
#
# Exits 0 with `OK: N evidence files validated` on success.
# Exits 1 with the list of failures otherwise.
#
# Designed to run from repo root with no extra dependencies (awk + grep + sed).

set -euo pipefail

REQ_FILE=".planning/REQUIREMENTS.md"
PLANNING_ROOT=".planning"

if [[ ! -f "${REQ_FILE}" ]]; then
  echo "FAIL: ${REQ_FILE} not found (run from repo root)" >&2
  exit 1
fi

REQUIRED_KEYS=("requirement_id" "status" "verified_at" "verified_by" "evidence")

# Collect every relative evidence path from REQUIREMENTS.md table rows.
#
# A "table row" is a line starting with `|` that contains an Evidence-column
# markdown link `[label](relative/path.md)`. We extract the path inside `()`,
# but only when the path looks like a planning-rooted evidence file
# (starts with `phases/` and ends with `.md`).
#
# Lines whose Evidence column reads `Phase ... (planned)` or
# `Phase ... (archive ...)` carry no markdown link and are naturally skipped.

mapfile -t EVIDENCE_PATHS < <(
  grep -E '^\|' "${REQ_FILE}" \
    | grep -oE '\[[^]]+\]\(phases/[^)]+\.md\)' \
    | sed -E 's/^\[[^]]+\]\(([^)]+)\)$/\1/' \
    | sort -u
)

FAILURES=()
VALIDATED=0

for rel in "${EVIDENCE_PATHS[@]}"; do
  abs="${PLANNING_ROOT}/${rel}"

  if [[ ! -f "${abs}" ]]; then
    FAILURES+=("missing file: ${abs}")
    continue
  fi

  # Frontmatter is the YAML block between the first two `---` lines.
  fm=$(awk '
    BEGIN { in_fm = 0; done = 0 }
    /^---[[:space:]]*$/ {
      if (in_fm == 0 && done == 0) { in_fm = 1; next }
      if (in_fm == 1)              { in_fm = 0; done = 1; exit }
    }
    in_fm == 1 { print }
  ' "${abs}")

  if [[ -z "${fm}" ]]; then
    FAILURES+=("missing frontmatter: ${abs}")
    continue
  fi

  missing_keys=()
  for key in "${REQUIRED_KEYS[@]}"; do
    if ! grep -qE "^${key}:" <<<"${fm}"; then
      missing_keys+=("${key}")
    fi
  done

  if (( ${#missing_keys[@]} > 0 )); then
    FAILURES+=("missing frontmatter keys [${missing_keys[*]}]: ${abs}")
    continue
  fi

  VALIDATED=$(( VALIDATED + 1 ))
done

if (( ${#FAILURES[@]} > 0 )); then
  echo "FAIL: requirements matrix validation failed" >&2
  for f in "${FAILURES[@]}"; do
    echo "  - ${f}" >&2
  done
  exit 1
fi

if (( VALIDATED == 0 )); then
  echo "FAIL: no resolvable evidence links found in ${REQ_FILE}" >&2
  exit 1
fi

echo "OK: ${VALIDATED} evidence files validated"
