#!/usr/bin/env bash
# deploy/oci/catch-a1.sh
#
# Polls Oracle OCI until a VM.Standard.A1.Flex instance is provisioned.
# OCI's free tier A1 capacity (arm64) is scarce; this script retries with
# exponential backoff across all availability domains in the home region,
# first attempting 1 OCPU/6 GB RAM and then 2 OCPU/12 GB RAM.
#
# Prerequisites:
#   - OCI CLI installed and configured (~/.oci/config or env vars)
#   - OCI_CLI_SUPPRESS_FILE_PERMISSIONS_WARNING=True if using instance principal
#
# Usage:
#   ./deploy/oci/catch-a1.sh [--compartment OCID] [--subnet OCID] \
#                             [--image OCID] [--ssh-key PATH] \
#                             [--region REGION] [--display-name NAME]
#
# Environment variables (all required unless passed as flags):
#   OCI_COMPARTMENT_ID   Compartment OCID
#   OCI_SUBNET_ID        Subnet OCID (in the target region)
#   OCI_IMAGE_ID         Boot image OCID (arm64-compatible, e.g. Oracle Linux 8)
#   OCI_SSH_KEY_FILE     Path to public SSH key file (or set OCI_SSH_KEY directly)
#   OCI_SSH_KEY          Public SSH key text (alternative to OCI_SSH_KEY_FILE)
#   OCI_REGION           OCI region identifier (default: from CLI config)
#   OCI_DISPLAY_NAME     Instance display name (default: hive-a1-staging)
#
# Shape sizing:
#   The script tries 1 OCPU/6 GB first (fits within the Always Free 2 OCPU/12 GB
#   ceiling and leaves headroom). On consecutive failure it escalates to 2 OCPU/12 GB.
#   Both sizes stay within the Always Free limit.
#
# Backoff:
#   Starts at BACKOFF_MIN seconds, doubles each cycle up to BACKOFF_MAX,
#   then holds at BACKOFF_MAX. Each cycle tries all ADs before sleeping.
#
# Safety:
#   - Idempotent: if an instance is already running with OCI_DISPLAY_NAME,
#     the script detects it and exits 0 without launching a duplicate.
#   - Safe to Ctrl-C at any time; no partial state is left because OCI
#     launches are atomic (the instance either starts or returns an error).

set -euo pipefail

# ---- defaults ---------------------------------------------------------------
COMPARTMENT_ID="${OCI_COMPARTMENT_ID:-}"
SUBNET_ID="${OCI_SUBNET_ID:-}"
IMAGE_ID="${OCI_IMAGE_ID:-}"
SSH_KEY="${OCI_SSH_KEY:-}"
SSH_KEY_FILE="${OCI_SSH_KEY_FILE:-}"
REGION="${OCI_REGION:-}"
DISPLAY_NAME="${OCI_DISPLAY_NAME:-hive-a1-staging}"

BACKOFF_MIN="${BACKOFF_MIN:-30}"    # seconds between first retry
BACKOFF_MAX="${BACKOFF_MAX:-300}"   # cap at 5 minutes
BACKOFF_FACTOR="${BACKOFF_FACTOR:-2}"
# Ensure BACKOFF_FACTOR is a positive integer to prevent arithmetic errors.
if ! [[ "${BACKOFF_FACTOR}" =~ ^[1-9][0-9]*$ ]]; then
  echo "ERROR: BACKOFF_FACTOR must be a positive integer, got: '${BACKOFF_FACTOR}'" >&2
  exit 1
fi

SHAPE="VM.Standard.A1.Flex"

# ---- flag parsing -----------------------------------------------------------
while [[ $# -gt 0 ]]; do
  case "$1" in
    --compartment) COMPARTMENT_ID="$2"; shift 2 ;;
    --subnet)      SUBNET_ID="$2"; shift 2 ;;
    --image)       IMAGE_ID="$2"; shift 2 ;;
    --ssh-key)     SSH_KEY_FILE="$2"; shift 2 ;;
    --region)      REGION="$2"; shift 2 ;;
    --display-name) DISPLAY_NAME="$2"; shift 2 ;;
    *)
      echo "Unknown flag: $1" >&2
      echo "Usage: $0 [--compartment OCID] [--subnet OCID] [--image OCID]" \
           "[--ssh-key PATH] [--region REGION] [--display-name NAME]" >&2
      exit 1
      ;;
  esac
done

# ---- validate required inputs -----------------------------------------------
missing=()
[[ -z "${COMPARTMENT_ID}" ]] && missing+=("OCI_COMPARTMENT_ID / --compartment")
[[ -z "${SUBNET_ID}" ]]      && missing+=("OCI_SUBNET_ID / --subnet")
[[ -z "${IMAGE_ID}" ]]       && missing+=("OCI_IMAGE_ID / --image")

# Resolve SSH key text
if [[ -z "${SSH_KEY}" && -n "${SSH_KEY_FILE}" ]]; then
  if [[ ! -f "${SSH_KEY_FILE}" ]]; then
    echo "ERROR: SSH key file not found: ${SSH_KEY_FILE}" >&2
    exit 1
  fi
  SSH_KEY="$(cat "${SSH_KEY_FILE}")"
fi
[[ -z "${SSH_KEY}" ]] && missing+=("OCI_SSH_KEY or OCI_SSH_KEY_FILE / --ssh-key")

if [[ ${#missing[@]} -gt 0 ]]; then
  echo "ERROR: Missing required configuration:" >&2
  for m in "${missing[@]}"; do echo "  - ${m}" >&2; done
  exit 1
fi

# ---- region flag for CLI calls ----------------------------------------------
REGION_FLAG=()
if [[ -n "${REGION}" ]]; then
  REGION_FLAG=(--region "${REGION}")
fi

# ---- helpers ----------------------------------------------------------------
log() { echo "[$(date -u '+%Y-%m-%dT%H:%M:%SZ')] $*"; }

# Returns 0 and prints the instance OCID if an instance with OCI_DISPLAY_NAME
# already exists in RUNNING or PROVISIONING state.
check_existing() {
  local running provisioning combined
  running=$(oci compute instance list \
    "${REGION_FLAG[@]}" \
    --compartment-id "${COMPARTMENT_ID}" \
    --display-name   "${DISPLAY_NAME}" \
    --lifecycle-state RUNNING \
    --query 'data[*].id' \
    --raw-output \
    2>/dev/null || echo "[]")
  provisioning=$(oci compute instance list \
    "${REGION_FLAG[@]}" \
    --compartment-id "${COMPARTMENT_ID}" \
    --display-name   "${DISPLAY_NAME}" \
    --lifecycle-state PROVISIONING \
    --query 'data[*].id' \
    --raw-output \
    2>/dev/null || echo "[]")
  # Combine both arrays and return first hit, if any.
  combined=$(printf '%s\n%s\n' "${running}" "${provisioning}" \
    | grep -v '^\[\]$' | grep -v '^$' | head -1 || true)
  echo "${combined}"
}

# Attempt to launch a single instance.
# Args: $1=ocpus $2=memory_gb $3=availability_domain
# Returns 0 on success (prints instance OCID), non-zero on capacity error.
try_launch() {
  local ocpus="$1"
  local memory_gb="$2"
  local ad="$3"

  log "Trying ${ocpus} OCPU / ${memory_gb} GB in AD: ${ad}"

  # Capture both stdout (OCID on success) and stderr (error message).
  local output
  local rc=0
  output=$(oci compute instance launch \
    "${REGION_FLAG[@]}" \
    --compartment-id                   "${COMPARTMENT_ID}" \
    --availability-domain              "${ad}" \
    --display-name                     "${DISPLAY_NAME}" \
    --shape                            "${SHAPE}" \
    --shape-config                     "{\"ocpus\": ${ocpus}, \"memoryInGBs\": ${memory_gb}}" \
    --image-id                         "${IMAGE_ID}" \
    --subnet-id                        "${SUBNET_ID}" \
    --assign-public-ip                 true \
    --metadata                         "$(jq -n --arg k "${SSH_KEY}" '{"ssh_authorized_keys":$k}')" \
    --wait-for-state                   RUNNING \
    --max-wait-seconds                 600 \
    --query 'data.id' \
    --raw-output \
    2>&1) || rc=$?

  if [[ ${rc} -eq 0 ]]; then
    echo "${output}"
    return 0
  fi

  # OCI returns HTTP 500 with code InternalError for out-of-capacity.
  # Also covers LimitExceeded (429) if service limit is hit.
  if echo "${output}" | grep -qiE "Out of host capacity|InternalError|LimitExceeded|ServiceError"; then
    log "  Capacity unavailable in ${ad}: $(echo "${output}" | grep -oiE 'Out of host capacity|InternalError|LimitExceeded' | head -1)"
    return 1
  fi

  # Unexpected error: log and propagate.
  log "ERROR: unexpected OCI error:"
  echo "${output}" >&2
  return "${rc}"
}

# ---- list availability domains ----------------------------------------------
log "Fetching availability domains for compartment ${COMPARTMENT_ID}..."
ADS=$(oci iam availability-domain list \
  "${REGION_FLAG[@]}" \
  --compartment-id "${COMPARTMENT_ID}" \
  --query 'data[*].name' \
  --raw-output \
  2>/dev/null | tr -d '[]"' | tr ',' ' ')

if [[ -z "${ADS}" ]]; then
  log "ERROR: Could not list availability domains. Check OCI CLI config and compartment OCID." >&2
  exit 1
fi

log "Availability domains: ${ADS}"

# ---- check for existing instance -------------------------------------------
log "Checking for existing instance with display name '${DISPLAY_NAME}'..."
EXISTING=$(check_existing)
if [[ -n "${EXISTING}" ]]; then
  log "Instance already exists: ${EXISTING}"
  log "Nothing to do. Exiting 0."
  exit 0
fi

# ---- sizing ladder ----------------------------------------------------------
# Try 1 OCPU/6 GB first, then 2 OCPU/12 GB.
declare -a SIZES=(
  "1 6"
  "2 12"
)

# ---- main retry loop --------------------------------------------------------
backoff="${BACKOFF_MIN}"
attempt=0

log "Starting catch loop for ${SHAPE}. Ctrl-C to stop."

while true; do
  attempt=$(( attempt + 1 ))
  log "=== Attempt ${attempt} ==="

  for size_spec in "${SIZES[@]}"; do
    read -r ocpus memory_gb <<< "${size_spec}"

    for ad in ${ADS}; do
      instance_id=$(try_launch "${ocpus}" "${memory_gb}" "${ad}") && {
        log "SUCCESS! Instance provisioned."
        log "  OCID        : ${instance_id}"
        log "  Size        : ${ocpus} OCPU / ${memory_gb} GB"
        log "  AD          : ${ad}"
        log "  Display name: ${DISPLAY_NAME}"
        log ""
        log "Next steps:"
        log "  1. Find the public IP: oci compute instance list-vnics --instance-id ${instance_id}"
        log "  2. SSH: ssh -i <private_key> opc@<public_ip>"
        log "  3. Run the Hive enterprise stack: docker compose --profile enterprise up -d"
        exit 0
      } || true  # non-zero means capacity error; continue to next AD/size
    done
  done

  log "All ADs exhausted for this attempt. Sleeping ${backoff}s before retry..."
  sleep "${backoff}"

  # Exponential backoff with cap.
  backoff=$(( backoff * BACKOFF_FACTOR ))
  if [[ ${backoff} -gt ${BACKOFF_MAX} ]]; then
    backoff="${BACKOFF_MAX}"
  fi
done
