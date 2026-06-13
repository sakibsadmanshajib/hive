#!/usr/bin/env bash
# deploy/gcp/provision.sh
#
# Provision the Hive demo VM on Google Cloud Platform.
#
# Creates an e2-standard-2 instance (2 vCPU, 8 GB RAM) in the
# asia-southeast1 (Singapore) region running Ubuntu 22.04 LTS, then
# installs Docker Engine with the Compose plugin.
#
# IDEMPOTENT: re-running this script when the instance already exists
# is safe. The instance-create step detects the existing resource and
# exits with a warning (non-fatal) so the rest of the script continues.
#
# Cloudflare Tunnel note
# ─────────────────────
# With Cloudflare Tunnel for ingress, NO inbound firewall ports need to be
# opened. The tunnel agent (cloudflared) inside the VM connects OUTBOUND to
# Cloudflare's edge over HTTPS (port 443), which standard GCP firewall rules
# already permit. Removing inbound 80/443 entries is the recommended
# posture; this script follows that guidance.
#
# Usage
# ─────
#   export GCP_PROJECT=my-project-id   # or pass --project
#   export GCP_ZONE=asia-southeast1-b  # or pass --zone (default: asia-southeast1-b)
#   export INSTANCE_NAME=hive-demo     # optional, defaults to hive-demo
#   bash deploy/gcp/provision.sh [--project PROJECT] [--zone ZONE] [--name NAME]
#
# Requirements: gcloud CLI authenticated with sufficient IAM permissions
#   (compute.instances.create, compute.disks.create, compute.firewalls.get).

set -euo pipefail

# ── Defaults ─────────────────────────────────────────────────────────────────
PROJECT="${GCP_PROJECT:-}"
ZONE="${GCP_ZONE:-asia-southeast1-b}"
INSTANCE_NAME="${INSTANCE_NAME:-hive-demo}"
MACHINE_TYPE="e2-standard-2"
IMAGE_FAMILY="ubuntu-2204-lts"
IMAGE_PROJECT="ubuntu-os-cloud"
DISK_SIZE="30GB"
# SSH source range for the firewall rule. Default 0.0.0.0/0 allows SSH from
# anywhere; restrict to your office/VPN CIDR in production, e.g. 203.0.113.0/24.
SSH_SOURCE_RANGE="${SSH_SOURCE_RANGE:-0.0.0.0/0}"

# ── Argument parsing ──────────────────────────────────────────────────────────
while [[ $# -gt 0 ]]; do
  case "$1" in
    --project) PROJECT="$2"; shift 2 ;;
    --zone)    ZONE="$2";    shift 2 ;;
    --name)    INSTANCE_NAME="$2"; shift 2 ;;
    *) echo "Unknown flag: $1"; exit 1 ;;
  esac
done

if [[ -z "$PROJECT" ]]; then
  echo "ERROR: GCP_PROJECT env var or --project flag is required." >&2
  exit 1
fi

REGION="${ZONE%-*}"   # e.g. asia-southeast1 from asia-southeast1-b

echo "==> Provisioning Hive demo VM"
echo "    Project : $PROJECT"
echo "    Zone    : $ZONE"
echo "    Name    : $INSTANCE_NAME"
echo "    Machine : $MACHINE_TYPE"
echo "    Image   : $IMAGE_FAMILY ($IMAGE_PROJECT)"
echo ""

# ── Step 1: Create the VM ─────────────────────────────────────────────────────
# gcloud returns exit code 1 if the instance already exists; we treat that
# as a warning and continue rather than aborting.
if gcloud compute instances describe "$INSTANCE_NAME" \
     --project="$PROJECT" --zone="$ZONE" &>/dev/null; then
  echo "==> VM '$INSTANCE_NAME' already exists — skipping creation."
else
  echo "==> Creating VM '$INSTANCE_NAME' ..."
  gcloud compute instances create "$INSTANCE_NAME" \
    --project="$PROJECT" \
    --zone="$ZONE" \
    --machine-type="$MACHINE_TYPE" \
    --image-family="$IMAGE_FAMILY" \
    --image-project="$IMAGE_PROJECT" \
    --boot-disk-size="$DISK_SIZE" \
    --boot-disk-type="pd-balanced" \
    --tags="hive-demo" \
    --metadata=enable-oslogin=TRUE \
    --scopes=cloud-platform
  echo "==> VM created."
fi

# ── Step 2: Firewall — SSH only (Cloudflare Tunnel handles external HTTPS) ───
# With Cloudflare Tunnel, no inbound 80/443 rules are required.
# We only ensure the default SSH rule (tcp:22) exists on the tag.
# If your project already has a default-allow-ssh rule, this is a no-op.
FIREWALL_RULE="hive-demo-allow-ssh"
if gcloud compute firewall-rules describe "$FIREWALL_RULE" \
     --project="$PROJECT" &>/dev/null; then
  echo "==> Firewall rule '$FIREWALL_RULE' already exists — skipping."
else
  echo "==> Creating firewall rule for SSH access ..."
  gcloud compute firewall-rules create "$FIREWALL_RULE" \
    --project="$PROJECT" \
    --direction=INGRESS \
    --priority=1000 \
    --network=default \
    --action=ALLOW \
    --rules=tcp:22 \
    --source-ranges="$SSH_SOURCE_RANGE" \
    --target-tags="hive-demo"
  echo "==> Firewall rule created."
fi

# ── Step 3: Install Docker + Compose plugin on the VM ────────────────────────
# We use a startup script sent via gcloud ssh so the installation is
# idempotent (apt-get is a no-op if Docker is already installed).
echo "==> Installing Docker Engine + Compose plugin on the VM ..."
gcloud compute ssh "$INSTANCE_NAME" \
  --project="$PROJECT" \
  --zone="$ZONE" \
  --command='
set -euo pipefail

echo "--- Installing Docker Engine ---"
if command -v docker &>/dev/null; then
  echo "Docker already installed: $(docker --version)"
else
  # Official Docker install for Ubuntu 22.04 (from docs.docker.com/engine/install/ubuntu)
  sudo apt-get update -qq
  sudo apt-get install -y -qq \
    ca-certificates curl gnupg lsb-release
  sudo install -m 0755 -d /etc/apt/keyrings
  curl -fsSL https://download.docker.com/linux/ubuntu/gpg \
    | sudo gpg --dearmor -o /etc/apt/keyrings/docker.gpg
  sudo chmod a+r /etc/apt/keyrings/docker.gpg
  echo \
    "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/docker.gpg] \
    https://download.docker.com/linux/ubuntu \
    $(lsb_release -cs) stable" \
    | sudo tee /etc/apt/sources.list.d/docker.list > /dev/null
  sudo apt-get update -qq
  sudo apt-get install -y -qq \
    docker-ce docker-ce-cli containerd.io \
    docker-buildx-plugin docker-compose-plugin
  sudo usermod -aG docker "$USER"
  echo "Docker installed: $(docker --version)"
  echo "Compose plugin: $(docker compose version)"
fi

echo "--- Done ---"
'
echo ""
echo "==> Provisioning complete."
echo ""
echo "Next steps:"
echo "  1. Copy the repo + .env to the VM:"
echo "       gcloud compute scp --recurse . $INSTANCE_NAME:~/hive --project=$PROJECT --zone=$ZONE"
echo "  2. SSH into the VM:"
echo "       gcloud compute ssh $INSTANCE_NAME --project=$PROJECT --zone=$ZONE"
echo "  3. See deploy/gcp/README.md for the full bring-up runbook."
