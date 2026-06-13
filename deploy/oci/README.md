# OCI A1 Flex Staging Runbook

Hive's free-forever staging target is an Oracle OCI A1 Flex instance (arm64, 2 OCPU / 12 GB RAM). OCI free-tier A1 capacity is frequently sold out; `catch-a1.sh` polls until a slot becomes available.

## Prerequisites

1. **OCI CLI** installed and authenticated (`oci setup config` or `~/.oci/config`).
2. An SSH key pair. The public key is injected into the instance at launch.
3. The following OCIDs from your OCI tenancy:
   - Compartment OCID
   - Subnet OCID (in the target region, with public IP assignment enabled)
   - Boot image OCID (arm64-compatible; Oracle Linux 8 or Ubuntu 22.04 for aarch64)

## Running the catch script

```bash
# Export required variables
export OCI_COMPARTMENT_ID="ocid1.compartment.oc1..aaaa..."
export OCI_SUBNET_ID="ocid1.subnet.oc1.ap-mumbai-1.aaaa..."
export OCI_IMAGE_ID="ocid1.image.oc1..aaaa..."
export OCI_SSH_KEY_FILE="$HOME/.ssh/id_ed25519.pub"
export OCI_REGION="ap-mumbai-1"          # optional if set in ~/.oci/config
export OCI_DISPLAY_NAME="hive-a1-staging" # default if omitted

# Start the catch loop (runs until an instance is provisioned or you Ctrl-C)
chmod +x deploy/oci/catch-a1.sh
./deploy/oci/catch-a1.sh
```

All variables can also be passed as flags:

```bash
./deploy/oci/catch-a1.sh \
  --compartment ocid1.compartment.oc1..aaaa... \
  --subnet      ocid1.subnet.oc1.ap-mumbai-1.aaaa... \
  --image       ocid1.image.oc1..aaaa... \
  --ssh-key     ~/.ssh/id_ed25519.pub \
  --region      ap-mumbai-1
```

## What the script does

1. Lists all availability domains (ADs) in the region.
2. Checks whether an instance named `OCI_DISPLAY_NAME` already exists (idempotent).
3. Cycles through ADs, trying 1 OCPU/6 GB first, then 2 OCPU/12 GB.
4. On "Out of host capacity" (HTTP 500 / InternalError), sleeps and retries with exponential backoff (30 s default, doubles each cycle, capped at 300 s).
5. On success, prints the instance OCID and connection instructions, then exits 0.

## Backoff tuning

| Variable | Default | Description |
|----------|---------|-------------|
| `BACKOFF_MIN` | `30` | Initial sleep in seconds |
| `BACKOFF_MAX` | `300` | Maximum sleep cap in seconds |
| `BACKOFF_FACTOR` | `2` | Multiplier per cycle |

## After provisioning

```bash
# 1. Find the public IP
oci compute instance list-vnics \
  --instance-id <INSTANCE_OCID> \
  --query 'data[0]."public-ip"' --raw-output

# 2. SSH into the instance
ssh -i ~/.ssh/id_ed25519 opc@<PUBLIC_IP>

# 3. Install Docker (Oracle Linux 8)
sudo dnf install -y docker
sudo systemctl enable --now docker
sudo usermod -aG docker opc
# Log out and back in so the group takes effect.

# 4. Clone the repo and start the Hive enterprise stack
git clone https://github.com/sakibsadmanshajib/hive.git
cd hive
cp .env.example .env   # fill in all required variables
cd deploy/docker
docker compose --env-file ../../.env --profile enterprise up -d
```

## Multi-arch image builds

Before deploying to the A1 instance, build and push the arm64 images:

```bash
# From the repo root
export HIVE_REGISTRY="ghcr.io/sakibsadmanshajib/hive"
export HIVE_TAG="$(git rev-parse --short HEAD)"

# Build for both amd64 and arm64, push to registry
./deploy/docker/buildx.sh --push

# On the A1 instance, pull and run the arm64 variant automatically
# (Docker on arm64 pulls the arm64 manifest from the multi-arch image)
```

See `deploy/docker/buildx.sh --help` for all options.
