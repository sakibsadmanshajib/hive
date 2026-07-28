# Cloudflare Tunnel ingress

The demo box has no open inbound ports. Every public hostname reaches it through
a single Cloudflare Tunnel, and `cloudflared` on the box runs as a **user**
systemd unit at `~/.config/systemd/user/cloudflared.service`:

```ini
[Service]
EnvironmentFile=%h/.cloudflared/env
ExecStart=/usr/local/bin/cloudflared --no-autoupdate --loglevel info tunnel run
Restart=always
RestartSec=5
```

`~/.cloudflared/env` holds only `TUNNEL_TOKEN`. It is a user unit rather than a
system one, so `systemctl status cloudflared` finds nothing; use
`systemctl --user status cloudflared` (or `journalctl --user -u cloudflared`).
`loginctl show-user sakib` reports `Linger=yes`, which is what keeps the tunnel
up across logouts.

There is no `--config` flag and no local config file, which means the tunnel is
**remotely managed**: its ingress rules are stored in Cloudflare and pulled at
connect time. Nothing about the hostname map lived in this repository, so it was
invisible to code review and could not be redeployed from source.

`tunnel-ingress.json` closes that gap. It is the single source of truth for which
hostnames are public and what each one reaches.

## Public hostnames

| Hostname                   | Origin           | Service                                  |
| -------------------------- | ---------------- | ---------------------------------------- |
| `api-hive.scubed.co`       | `localhost:8080` | edge-api                                 |
| `control-hive.scubed.co`   | `localhost:8081` | control-plane                            |
| `chat-hive.scubed.co`      | `localhost:3003` | Caddy in front of Open WebUI             |
| `artifacts-hive.scubed.co` | `localhost:3004` | Caddy artifacts host                     |
| `console-hive.scubed.co`   | `localhost:3005` | Caddy in front of the web-console container |
| anything else              | —                | `http_status:404`                        |

`hive.scubed.co` is the marketing site on Cloudflare Pages and is not served by
the tunnel.

## The control-plane hostname

`control-hive.scubed.co` is the control-plane's one public hostname. It is public
deliberately: payment-rail webhooks and customer redirects have to reach it from
the internet, which is what `CONTROL_PLANE_PUBLIC_URL` feeds
(`resolveCallbackBaseURL` in `apps/control-plane/internal/payments/http.go`).

Being public does not expose the service-to-service surface. Every `/internal/*`
route is wrapped in `RequireInternalToken`, which does a constant-time compare
and denies unconditionally when no token is configured, so it fails closed rather
than open. Verified from the public internet:

```
/internal/routing/select            -> 401
/internal/apikeys/resolve           -> 401
/internal/accounting/reservations   -> 401
/internal/catalog/snapshot          -> 401
/internal/providers                 -> 401
/internal/license/entitlement       -> 401
/internal/rag/ingest                -> 401
/metrics                            -> 404
/health                             -> 200
```

`/internal/auth/user-created` is a Supabase Database Webhook target and is
intentionally unauthenticated at the middleware layer; the handler verifies an
`X-Hive-Signup-Secret` header itself. Because the token guard already holds at
the application layer, no proxy-level path blocking is needed for the tunnel the
way `Caddyfile.owui` blocks Open WebUI admin paths.

The web-console Worker reaches control-plane over this same public hostname via
`CONTROL_PLANE_BASE_URL` in `apps/web-console/wrangler.jsonc`. There is no
private path between the Worker and the box, so retiring the public hostname is
not an option.

## Retired: `cp-hive.scubed.co`

`cp-hive.scubed.co` was the control-plane hostname of an earlier VM
(`40.233.86.19`, the 1 GB instance that `docker-compose.staging.yml` describes).
That VM is gone. The DNS record outlived it as a **proxied A record with no
tunnel ingress rule**, so Cloudflare kept trying to open a connection to a dead
origin and every request returned 522 after a long hang.

That is worse than a hostname that does not exist, because it reads as an outage
of a healthy service. The record was deleted on 2026-07-27. The name now fails
DNS resolution outright, and any future stray hostname hits the tunnel catch-all
and gets a clean 404 instead of hanging.

`cp-hive.scubed.co` must never be reintroduced.
`apps/web-console/tests/unit/control-plane-host.test.ts` enforces that in the
required unit check, with no credentials and no network: it pins the
control-plane hostname across the files that write it down, and it scans every
tracked text file under `deploy/` and fails on any `scubed.co` hostname that
`tunnel-ingress.json` does not declare. This directory is deliberately excluded
from that scan, because it is the registry and has to be able to name retired
hostnames in prose.

## Checking and applying

```bash
export CLOUDFLARE_API_TOKEN=...    # check: Zone:DNS:Read + Account:Tunnel:Read
                                   # apply: also Account:Cloudflare Tunnel:Edit
export CLOUDFLARE_ACCOUNT_ID=...
export CLOUDFLARE_ZONE_ID=...

npm run cloudflare:check           # diff this repo against live Cloudflare
npm run cloudflare:apply           # print the plan, confirm, then push ingress
npm run cloudflare:apply -- --yes  # same, non-interactive (CI or scripted runs)
```

`cloudflare:check` fails when the live ingress and the spec disagree, when a
hostname with an ingress rule has no proxied CNAME to the tunnel, or when an
address record (`A`, `AAAA` or `CNAME`) under `-hive.scubed.co` exists with no
ingress rule behind it. That last check is the one that catches the `cp-hive`
class of defect, and it reports the exact record id to act on. It ignores
non-address record types, because a `TXT`, `CAA` or `MX` record under a managed
name does not resolve to an origin and so cannot produce the 522.

## What `--apply` does, in order

The configurations endpoint replaces tunnel ingress **wholesale**, so a live rule
that is missing from `tunnel-ingress.json` is deleted by the write. `--apply`
therefore never writes first:

1. Reads live ingress and prints the full plan, listing every rule it would add,
   change, `REMOVE`, or `DROP` (a top-level config key present live and absent
   from the spec).
2. Stops if the plan is empty and issues no write at all, so a repeated apply is
   a genuine no-op rather than a rewrite.
3. Otherwise waits for the operator to type `apply`. Without a terminal it
   refuses unless `--yes` was passed, so it cannot silently destroy rules from a
   pipeline.
4. Writes, then re-reads and fails if live ingress still differs from the spec.

`cloudflare:apply` writes tunnel ingress only, in every mode. DNS records are
never created or deleted, because deleting a DNS record is not something to retry
your way out of. The check prints what to change and a human does it.

The token is only needed for these commands and is not required to deploy the
box, so `cloudflare:check` is intentionally not wired into `ci.yml`: it needs
credentials that pull-request CI does not have. Run it after any hostname change
and when auditing what is publicly reachable.
