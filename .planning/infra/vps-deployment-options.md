# VPS Deployment Options — Staging & EnterpriseEdge

**Status:** Decision document — research only, no infra changes  
**Date:** 2026-06-12  
**Author:** CTO research session  

---

## Context

Current staging box: OCI VM.Standard.E2.1.Micro (AMD, 1 GB RAM, Always Free).  
Pain point: 1 GB is too small to run the `chat` or `enterprise` profile alongside the API-only `cloud` profile. Open WebUI + Caddy requires ~2 GB on its own.

The web-console (Next.js) is deployed to **Cloudflare Workers** via OpenNext — it does NOT run on the VM and does not factor into sizing.  
Redis is **external Upstash** on the cloud/staging profile — also not on the VM.

---

## Part 1 — Stack Sizing

### Service RAM budgets (observed limits from `docker-compose.staging.yml` + upstream defaults)

| Service | Profile(s) | Confirmed limit / estimate |
|---|---|---|
| edge-api (Go) | cloud, enterprise | 180 MB (staging mem_limit) |
| control-plane (Go) | cloud, enterprise | 180 MB (staging mem_limit) |
| litellm (Python) | cloud, enterprise | 420 MB (staging mem_limit); prod peak ~500 MB |
| redis:alpine | enterprise, local | ~30 MB |
| open-webui | enterprise, chat | **2 GB** (mem_limit in compose) |
| caddy:alpine | enterprise, chat | ~30 MB |
| OS + Docker daemon | all | ~250–350 MB |

### RAM floor by deployment tier

| Tier | Profile | Services on VM | RAM floor | Recommended box |
|---|---|---|---|---|
| **(a) Staging API-only** | `cloud` | edge-api + control-plane + litellm | **~1.1 GB** | 2 GB (current 1 GB is at the limit) |
| **(b) Staging + chat UI** | `cloud` + `chat` | above + open-webui + caddy | **~3.2 GB** | **4 GB minimum** |
| **(c) Enterprise demo + Ollama** | `enterprise` | all above + redis + ollama | **8 GB + model weights** | **16 GB+** (Ollama: 7B model ~4–5 GB, 13B ~8 GB) |

Notes:
- Tier (a): current OCI Micro is technically surviving but has no headroom; any litellm warm-up spike OOMs.
- Tier (b): 4 GB is the practical floor; 8 GB is comfortable.
- Tier (c): Ollama model RAM is additive and not in the Docker mem_limit. A single 7B model at Q4 quantisation needs ~4.5 GB VRAM/RAM. Total for a comfortable demo: 16 GB.

---

## Part 2 — VPS Market Survey (June 2026, live-verified prices)

### Architecture note

**CI builds `linux/amd64` only** (`platforms: linux/amd64` in `deploy-staging.yml`).  
ARM VPS (Hetzner CAX, Oracle A1) requires adding `linux/arm64` to the build matrix — a small but real migration cost. Upstream images (open-webui, litellm, caddy, redis) all publish multi-arch manifests; only the Hive Go images need the pipeline change.

### Option table

| Provider / SKU | RAM | vCPU | Disk | Egress | Price/mo (USD) | Arch | Notes |
|---|---|---|---|---|---|---|---|
| **Oracle A1 Flex** (4 OCPU / 24 GB) | 24 GB | 4 | 200 GB block | 10 TB free | **$0** | ARM64 | Always Free — confirmed active June 2026 per Oracle docs. Reclaimed if CPU/net/mem all <20% for 7 days; keep a cron ping. New account required if not already on OCI. |
| **Hetzner CX32** | 8 GB | 4 | 80 GB | 20 TB | ~EUR 8.49 (~**$9.20**) | x86 (AMD) | Source: hetzner.com/cloud regular-performance tier. No pipeline change. Reliable EU infra. |
| **Contabo Cloud VPS 10** | 8 GB | 4 | 75 GB NVMe | Unlimited | **$4.40/mo** (12-mo term) | x86 | Source: contabo.com/en/vps, June 2026. Cheapest x86 8 GB on the market. Reputation for overselling; support is slower. |
| **Contabo Cloud VPS 20** | 12 GB | 6 | 100 GB NVMe | Unlimited | **$6.00/mo** (12-mo term) | x86 | Best-selling plan per Contabo. EUR 7.50 list, ~$8.15 month-to-month. |
| **OVH VPS-2 (2027 range)** | 8 GB | 4 | 75 GB NVMe | Unlimited | **$11.64/mo** (12-mo) | x86 | Source: ovhcloud.com/en-ca/vps. Canadian pricing. Daily backup included. |
| **OVH VPS-3** | 12 GB | 6 | 100 GB NVMe | Unlimited | **$16.83/mo** (12-mo) | x86 | 1 Gbps bandwidth, daily backup. |
| **DigitalOcean Basic 4 GB** | 4 GB | 2 | 80 GB | 4 TB | **$24/mo** | x86 | Source: digitalocean.com/pricing/droplets. Notably more expensive; good DX but poor value here. |
| **Hetzner CAX21** | 8 GB | 4 | 80 GB | 20 TB | ~EUR 6.90 (~**$7.50**) | ARM64 | Cost-optimized ARM. Requires adding arm64 to CI. |

### Eliminated options

- **DigitalOcean**: $24/mo for 4 GB does not fit budget. Their 8 GB is $48/mo.
- **Scaleway PLAY2-MICRO** (2 vCPU / 4 GB): ~EUR 4.99 — too small for chat tier.
- **AWS / GCP**: ruled out by owner preference.
- **Netcup**: good value but less mainstream; skip for now.

---

## Part 3 — Recommendation

### Primary: Oracle OCI A1 Flex 4 OCPU / 24 GB — $0/month

**Reasoning:**

1. **Confirmed Always Free as of June 2026.** Oracle's official documentation explicitly lists "Arm-based Ampere A1 Compute — 3,000 OCPU hours and 18,000 GB hours per month" (equivalent to one 4-OCPU / 24 GB instance) as an Always Free resource with no expiry. Source: docs.oracle.com/iaas/Content/FreeTier/freetier_topic-Always_Free_Resources.htm
2. **24 GB handles all three tiers,** including enterprise demo with a 7B Ollama model (~4.5 GB), with headroom.
3. **The current staging box is already OCI** — the SSH deploy workflow (`deploy@$STAGING_HOST`) transfers trivially: update one GitHub secret (`STAGING_HOST`), provision the new instance, copy `/opt/hive/` state.
4. **ARM pipeline cost is one PR:** add `linux/arm64` alongside `linux/amd64` in `deploy-staging.yml`. Docker Buildx + GitHub Actions cache handles this; build time increases ~2 min.
5. **Self-managed SSH deploy matches the EnterpriseEdge product goal.** The CI workflow already demonstrates the pattern: GitHub Actions pushes images to GHCR, SSHes to the box, and runs `docker compose up -d`. Customers buying EnterpriseEdge will use the same pattern. Running it yourselves is the best way to find friction.

**Risks of Oracle A1 Free:**
- "Out of host capacity" errors when provisioning in popular regions — use Ashburn or Phoenix at off-peak hours, or provision in AP regions.
- Idle reclamation (CPU + net + mem all <20% for 7 days): mitigate with a lightweight cron health-check ping.
- Oracle could change the Always Free terms; this has not happened since A1 launched in 2021 but is a non-zero risk.
- ARM requires the CI pipeline change described above.

### Fallback: Hetzner CX32 — ~$9.20/month (x86, no pipeline change)

**Reasoning:** If Oracle provisioning fails, ARM migration is deferred, or the team wants a paid option with cleaner SLA, Hetzner CX32 (4 vCPU / 8 GB AMD / 80 GB SSD / 20 TB egress) at ~EUR 8.49/month is the best-value x86 option. Zero pipeline changes required. Hetzner has a strong reliability track record and GDPR-compliant EU hosting. 8 GB comfortably handles tier (b) (API + chat UI). For tier (c) with Ollama you'd need to upgrade to CX42 (16 GB, ~EUR 17/month) — still well within $50/month budget.

### Budget summary

| Scenario | Primary (OCI A1) | Fallback (Hetzner CX32) |
|---|---|---|
| Staging API-only | $0 | ~$9.20 |
| Staging + chat UI | $0 | ~$9.20 |
| Enterprise demo + 7B Ollama | $0 | $0 (upgrade to CX42 ~$18.50) |
| Remaining budget headroom | $50 | $40.80 |

### Migration effort

From current OCI Micro to new OCI A1 (same account / new instance):

1. Provision new A1 Flex instance (4 OCPU / 24 GB) in OCI Console — ~10 min.
2. Add SSH public key, open ports 80/443 (Caddy), update `STAGING_HOST` secret — ~5 min.
3. Add `linux/arm64` to `build-push-action` matrix in `deploy-staging.yml` — ~15 min, one PR.
4. Trigger deploy workflow; smoke test — ~10 min.
5. Decommission old Micro instance.

**Total estimated effort: 1–2 hours including review.**

---

## Sources (live, June 2026)

- Oracle Always Free specs: https://docs.oracle.com/en-us/iaas/Content/FreeTier/freetier_topic-Always_Free_Resources.htm
- Oracle Free Tier overview: https://www.oracle.com/cloud/free/
- Hetzner Cloud pricing: https://www.hetzner.com/cloud/cost-optimized and https://www.hetzner.com/cloud/regular-performance
- Contabo VPS pricing: https://contabo.com/en/vps/
- OVH VPS 2027 range (CA): https://www.ovhcloud.com/en-ca/vps/
- DigitalOcean Droplets: https://www.digitalocean.com/pricing/droplets

---

🤖 Generated with [Claude Code](https://claude.com/claude-code)
