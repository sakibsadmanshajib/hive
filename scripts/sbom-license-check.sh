#!/usr/bin/env bash
# sbom-license-check.sh -- Generate a licence manifest for Go modules and npm
# packages, then fail if any dependency is AGPL or GPL.
#
# Usage:
#   ./scripts/sbom-license-check.sh [--report-only]
#
#   --report-only   Write the SBOM and report all findings without failing.
#                   Use this flag to audit the current tree without blocking CI.
#
# Output files (written to repo root):
#   SBOM-go.json      Go module licences as JSON array
#   SBOM-npm.json     npm package licences as JSON array
#   SBOM.json         Combined SPDX-2.3-compatible manifest
#
# Blocked licences (any SPDX variant):
#   GPL-1.0, GPL-2.0, GPL-3.0 and all -only/-or-later suffixes
#   AGPL-2.0, AGPL-3.0 and all -only/-or-later suffixes
#
# Adding a licence exception (allowlist):
#   See docs/license-allowlist.md for the required process.
#   Add the package to scripts/license-allowlist.json with written justification.
#
# Requirements:
#   go >= 1.21
#   github.com/google/go-licenses v1.6.0 (auto-installed if absent)
#   node >= 18, npm, npx

set -euo pipefail

REPORT_ONLY=false
for arg in "$@"; do
  case "$arg" in
    --report-only) REPORT_ONLY=true ;;
    *) echo "Unknown argument: $arg" >&2; exit 1 ;;
  esac
done

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
ALLOWLIST_FILE="${REPO_ROOT}/scripts/license-allowlist.json"
GO_SBOM="${REPO_ROOT}/SBOM-go.json"
NPM_SBOM="${REPO_ROOT}/SBOM-npm.json"
NPM_RAW_TMP=$(mktemp)
COMBINED_SBOM="${REPO_ROOT}/SBOM.json"
GO_CSV_TMP=$(mktemp)
trap 'rm -f "${GO_CSV_TMP}" "${NPM_RAW_TMP}"' EXIT

# SPDX identifiers that are blocked unconditionally.
BLOCKED_LICENSES=(
  "GPL-1.0"
  "GPL-2.0"
  "GPL-2.0-only"
  "GPL-2.0-or-later"
  "GPL-3.0"
  "GPL-3.0-only"
  "GPL-3.0-or-later"
  "AGPL-2.0"
  "AGPL-3.0"
  "AGPL-3.0-only"
  "AGPL-3.0-or-later"
)

echo "==> Licence clearance gate"
echo "    Repo root:   ${REPO_ROOT}"
echo "    Allowlist:   ${ALLOWLIST_FILE}"
echo "    Report-only: ${REPORT_ONLY}"
echo ""

# ---------------------------------------------------------------------------
# Helper: read allowlisted package names from scripts/license-allowlist.json
# ---------------------------------------------------------------------------
allowlisted_packages() {
  if [ -f "${ALLOWLIST_FILE}" ]; then
    node -e "
      const data = JSON.parse(require('fs').readFileSync('${ALLOWLIST_FILE}', 'utf8'));
      (data.packages || []).forEach(p => console.log(p.name));
    " 2>/dev/null || true
  fi
}

# ---------------------------------------------------------------------------
# Step 1: Go licences
# ---------------------------------------------------------------------------
echo "==> Scanning Go modules..."

GO_LICENSES_VERSION="v1.6.0"
if ! command -v go-licenses &>/dev/null; then
  echo "    Installing google/go-licenses@${GO_LICENSES_VERSION}..."
  go install "github.com/google/go-licenses@${GO_LICENSES_VERSION}"
fi

GO_MODULES=(
  "${REPO_ROOT}/apps/control-plane"
  "${REPO_ROOT}/apps/edge-api"
  "${REPO_ROOT}/packages/storage"
  "${REPO_ROOT}/packages/audit-canonical"
)

for mod_path in "${GO_MODULES[@]}"; do
  if [ ! -d "${mod_path}" ]; then
    echo "    WARNING: module path not found, skipping: ${mod_path}"
    continue
  fi
  mod_name=$(basename "${mod_path}")
  echo "    Scanning module: ${mod_name}"
  (
    cd "${mod_path}"
    # --ignore suppresses the first-party module itself so only third-party
    # deps appear in the output.
    go-licenses csv ./... \
      --ignore "github.com/sakibsadmanshajib/hive" \
      2>/dev/null
  ) >> "${GO_CSV_TMP}" || {
    echo "    WARNING: go-licenses returned non-zero for ${mod_name}."
  }
done

sort -u "${GO_CSV_TMP}" -o "${GO_CSV_TMP}"

# Convert CSV to JSON array. go-licenses CSV format: name,licenceURL,spdxId
node - "${GO_CSV_TMP}" "${GO_SBOM}" <<'EOF'
const fs = require('fs');
const [,, csvPath, outPath] = process.argv;
const lines = fs.readFileSync(csvPath, 'utf8').trim().split('\n').filter(Boolean);
const entries = lines.map(line => {
  const parts = line.split(',');
  return {
    name:      parts[0] || '',
    url:       parts[1] || '',
    spdxId:    (parts[2] || 'UNKNOWN').trim(),
    ecosystem: 'go',
  };
});
fs.writeFileSync(outPath, JSON.stringify(entries, null, 2));
console.log('    Go entries: ' + entries.length);
EOF

# ---------------------------------------------------------------------------
# Step 2: npm licences (web-console)
# ---------------------------------------------------------------------------
echo ""
echo "==> Scanning npm packages (apps/web-console)..."

WEB_CONSOLE="${REPO_ROOT}/apps/web-console"
if [ ! -d "${WEB_CONSOLE}/node_modules" ]; then
  echo "    node_modules not found -- running npm ci..."
  (cd "${WEB_CONSOLE}" && npm ci --ignore-scripts)
fi

# Pinned version of license-checker (MIT licensed, well-maintained).
# ponytail: npx --yes installs ephemerally; no persistent global install needed.
npx --yes license-checker@25.0.1 \
  --start "${WEB_CONSOLE}" \
  --excludePrivatePackages \
  --json > "${NPM_RAW_TMP}" 2>/dev/null

node - "${NPM_RAW_TMP}" "${NPM_SBOM}" <<'EOF'
const fs = require('fs');
const [,, rawPath, outPath] = process.argv;
const raw = JSON.parse(fs.readFileSync(rawPath, 'utf8'));
const entries = Object.entries(raw).map(([pkg, info]) => ({
  name:      pkg,
  url:       info.repository || '',
  spdxId:    (info.licenses || 'UNKNOWN').replace(/[()]/g, '').trim(),
  ecosystem: 'npm',
}));
fs.writeFileSync(outPath, JSON.stringify(entries, null, 2));
console.log('    npm entries: ' + entries.length);
EOF

# ---------------------------------------------------------------------------
# Step 3: Combine into SBOM.json
# ---------------------------------------------------------------------------
echo ""
echo "==> Writing combined SBOM..."

node - "${GO_SBOM}" "${NPM_SBOM}" "${COMBINED_SBOM}" <<'EOF'
const fs = require('fs');
const [,, goPath, npmPath, outPath] = process.argv;
const go  = JSON.parse(fs.readFileSync(goPath,  'utf8'));
const npm = JSON.parse(fs.readFileSync(npmPath, 'utf8'));
const combined = {
  spdxVersion: 'SPDX-2.3',
  dataLicence: 'CC0-1.0',
  name:        'hive-sovereign-edge',
  generatedAt: new Date().toISOString(),
  packages:    [...go, ...npm],
};
fs.writeFileSync(outPath, JSON.stringify(combined, null, 2));
console.log('    Total packages: ' + combined.packages.length);
EOF

# ---------------------------------------------------------------------------
# Step 4: Licence gate -- detect blocked SPDX IDs
# ---------------------------------------------------------------------------
echo ""
echo "==> Running licence gate..."

ALLOWLIST_NAMES=$(allowlisted_packages)
VIOLATION_COUNT=0
VIOLATION_LOG=""

# Stream each package from the combined SBOM through the checker.
while IFS=$'\t' read -r pkg_name pkg_spdx; do
  # Skip if explicitly allowlisted.
  if [ -n "${ALLOWLIST_NAMES}" ] && echo "${ALLOWLIST_NAMES}" | grep -qxF "${pkg_name}"; then
    echo "    ALLOWLISTED: ${pkg_name} (${pkg_spdx})"
    continue
  fi

  for blocked in "${BLOCKED_LICENSES[@]}"; do
    if echo "${pkg_spdx}" | grep -qiF "${blocked}"; then
      VIOLATION_COUNT=$((VIOLATION_COUNT + 1))
      VIOLATION_LOG="${VIOLATION_LOG}    * ${pkg_name}: ${pkg_spdx}\n"
      echo "    BLOCKED: ${pkg_name} -- ${pkg_spdx}"
      break
    fi
  done
done < <(node - "${COMBINED_SBOM}" <<'EOF'
const fs = require('fs');
const d = JSON.parse(fs.readFileSync(process.argv[1], 'utf8'));
d.packages.forEach(p => process.stdout.write((p.name || '') + '\t' + (p.spdxId || '') + '\n'));
EOF
)

# ---------------------------------------------------------------------------
# Step 5: Result
# ---------------------------------------------------------------------------
echo ""
if [ "${VIOLATION_COUNT}" -eq 0 ]; then
  echo "==> PASS -- No AGPL or GPL dependencies detected."
  echo "    SBOM written to: ${COMBINED_SBOM}"
  exit 0
else
  echo "==> FAIL -- ${VIOLATION_COUNT} blocked licence(s) found:"
  printf "%b" "${VIOLATION_LOG}"
  echo ""
  echo "    To resolve:"
  echo "    1. Replace the dependency with a permissively licensed alternative."
  echo "    2. If an exception is legally justified, add it to scripts/license-allowlist.json"
  echo "       following the process in docs/license-allowlist.md."
  echo "    3. All exceptions require written justification and reviewer approval."
  echo ""
  echo "    SBOM written to: ${COMBINED_SBOM}"
  if [ "${REPORT_ONLY}" = "true" ]; then
    echo "    (--report-only mode: exiting 0)"
    exit 0
  fi
  exit 1
fi
