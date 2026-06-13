# Hive Demo — GCP Deployment Runbook

Deploy the Hive cloud demo on a GCP e2-standard-2 VM (Singapore) with
Cloudflare Tunnel for zero-open-port ingress.

## Architecture

```
internet
   |
Cloudflare edge (TLS termination)
   |          |
api-hive.scubed.co   chat-hive.scubed.co
   |                      |
   +----------+  +---------+
              |  |
       [ GCP VM: e2-standard-2 / asia-southeast1 ]
       ┌──────────────────────────────────────────┐
       │  cloudflared (outbound tunnel agent)      │
       │  edge-api:8080                            │
       │  control-plane:8081                       │
       │  litellm:4000                             │
       │  open-webui:8080 (internal, chat profile) │
       └──────────────────────────────────────────┘

console-hive.scubed.co -> Cloudflare Workers (NOT this VM)
```

No inbound firewall ports are opened. Cloudflare Tunnel establishes
an outbound HTTPS connection from the VM to Cloudflare's edge.

## Step 1: Provision the VM

```bash
# Authenticate gcloud if not already done
gcloud auth login
gcloud config set project YOUR_GCP_PROJECT_ID

# Provision (idempotent, safe to re-run)
export GCP_PROJECT=YOUR_GCP_PROJECT_ID

# REQUIRED: the CIDR allowed to reach SSH (port 22). The script has no default
# and aborts if this is unset, so SSH is never accidentally opened to the whole
# internet. Use your office or VPN range, e.g. 203.0.113.0/24.
export SSH_SOURCE_RANGE=YOUR_OFFICE_OR_VPN_CIDR

bash deploy/gcp/provision.sh
```

The script creates an e2-standard-2 VM in `asia-southeast1-b` named
`hive-demo`, installs Docker Engine + the Compose plugin, and creates
a firewall rule that permits SSH only from `SSH_SOURCE_RANGE`. It does NOT
open ports 80 or 443 because Cloudflare Tunnel handles all external traffic.

`SSH_SOURCE_RANGE` is mandatory and has no default: if it is unset the script
exits with an error rather than exposing port 22 to `0.0.0.0/0`. For a
throwaway demo where world-open SSH is acceptable, set it explicitly to
`0.0.0.0/0` so the decision is recorded in your shell history.

## Step 2: Create the Cloudflare Tunnel

1. Log in to the Cloudflare Zero Trust dashboard.
2. Go to **Networks > Tunnels > Create a tunnel**.
3. Name it `hive-demo-sg`.
4. On the connector page, copy the **tunnel token** (the long string
   after `--token`).
5. Under the tunnel's **Public Hostnames** tab, add:
   - `api-hive.scubed.co` -> `http://edge-api:8080`
   - `chat-hive.scubed.co` -> `http://open-webui:8080`
6. Cloudflare automatically creates CNAME DNS records for each hostname.

## Step 3: Prepare .env on the VM

Copy the repo and a populated `.env` to the VM:

```bash
# From the repo root on your local machine
gcloud compute scp --recurse . hive-demo:~/hive \
  --project=YOUR_GCP_PROJECT_ID --zone=asia-southeast1-b
```

SSH into the VM and verify `.env` contains at minimum:

```
# Cloudflare Tunnel (from Step 2)
TUNNEL_TOKEN=<your-tunnel-token>

# Upstash Redis
REDIS_URL=rediss://default:<token>@<host>:6379

# Supabase
SUPABASE_URL=https://<project>.supabase.co
SUPABASE_ANON_KEY=<anon-key>
SUPABASE_SERVICE_ROLE_KEY=<service-role-key>
SUPABASE_DB_URL=postgresql://postgres:<password>@db.<project>.supabase.co:5432/postgres

# LLM providers
OPENROUTER_API_KEY=<your-openrouter-key>
GROQ_API_KEY=<your-groq-key>

# Shared internal secrets
LITELLM_MASTER_KEY=<random-32-chars>
CONTROL_PLANE_INTERNAL_TOKEN=<random-32-chars>
OWUI_SHIM_KEY=<openssl rand -base64 32>

# Chat URLs
HIVE_CHAT_URL=https://chat-hive.scubed.co
NEXT_PUBLIC_SUPABASE_URL=https://<project>.supabase.co
NEXT_PUBLIC_SUPABASE_ANON_KEY=<anon-key>
```

See `.env.example` in the repo root for the full list with inline
comments.

## Step 4: Bring up the stack

```bash
# SSH into the VM
gcloud compute ssh hive-demo --project=YOUR_GCP_PROJECT_ID --zone=asia-southeast1-b

# From the repo root on the VM
cd ~/hive
docker compose \
  -f deploy/docker/docker-compose.yml \
  -f deploy/docker/docker-compose.demo.yml \
  --env-file .env \
  --profile cloud --profile chat \
  up --build -d
```

Verify all services are healthy:

```bash
docker compose \
  -f deploy/docker/docker-compose.yml \
  -f deploy/docker/docker-compose.demo.yml \
  --env-file .env \
  --profile cloud --profile chat \
  ps

# Smoke test the API (from inside the VM)
curl -s http://localhost:8080/health
curl -s http://localhost:8081/health
```

## Step 5: Verify the tunnel

```bash
# Check cloudflared logs
docker logs hive-cloudflared-1 --tail 50

# Expected output includes:
#   "Registered tunnel connection" ...
#   "Connection established" ...
```

Then visit `https://api-hive.scubed.co/health` and
`https://chat-hive.scubed.co` from a browser to confirm end-to-end
connectivity.

## Required env vars summary

| Variable | Where to get it |
|---|---|
| `TUNNEL_TOKEN` | Cloudflare Zero Trust > Tunnels > connector token |
| `REDIS_URL` | Upstash console (use `rediss://` TLS URL) |
| `SUPABASE_URL` | Supabase project settings |
| `SUPABASE_ANON_KEY` | Supabase project settings > API |
| `SUPABASE_SERVICE_ROLE_KEY` | Supabase project settings > API |
| `OPENROUTER_API_KEY` | openrouter.ai > keys |
| `GROQ_API_KEY` | console.groq.com > API keys |
| `LITELLM_MASTER_KEY` | Generate: `openssl rand -hex 16` |
| `CONTROL_PLANE_INTERNAL_TOKEN` | Generate: `openssl rand -hex 16` |
| `OWUI_SHIM_KEY` | Generate: `openssl rand -base64 32` |

## Stopping the stack

```bash
docker compose \
  -f deploy/docker/docker-compose.yml \
  -f deploy/docker/docker-compose.demo.yml \
  --env-file .env \
  --profile cloud --profile chat \
  down
```

## Notes

- `console-hive.scubed.co` is served by Cloudflare Workers and does
  not run on this VM. Do not route it through the tunnel.
- The Caddy reverse proxy is NOT used in the demo profile. Cloudflare
  Tunnel terminates TLS and proxies directly to `open-webui:8080`.
- `RATE_LIMIT_FAIL_OPEN` should remain unset (default fail-closed) in
  this deployment.
