# Staging TLS Architecture

## Current State (as of 2026-06-12)

Both staging hostnames are Cloudflare-proxied (orange cloud). VM IP is not exposed in public DNS.

| Hostname | CF Record | Proxied | Origin |
|---|---|---|---|
| api-hive.scubed.co | A record, CF zone scubed.co | yes | VM port 8080 via Caddy |
| cp-hive.scubed.co | A record, CF zone scubed.co | yes | VM port 8081 via Caddy |

## TLS Layers

### Edge (CF to browser)

Cloudflare terminates TLS at the edge using its Universal SSL certificate (issued by Google Trust Services for `scubed.co`). Browsers see a CF-managed cert. Protocol: TLS 1.3, HTTP/2.

### Origin (CF to VM)

Caddy on the VM holds Let's Encrypt certificates for both hostnames (EC/P-256, issued by Let's Encrypt E7/E8). CF connects to origin on port 443 and validates the LE cert. This works correctly with zone SSL mode **Full (strict)**.

### Zone SSL Mode

**Owner action required:** The scoped `CLOUDFLARE_API_TOKEN` in `.env` has DNS Edit permission but not Zone Settings Write. Zone SSL mode must be set to `Full (strict)` via the Cloudflare dashboard:

1. Go to dash.cloudflare.com, select zone `scubed.co`.
2. SSL/TLS Overview: set encryption mode to **Full (strict)**.

This is safe to set immediately because Caddy holds valid public LE certs on the origin (verified: notAfter Jul 23 2026, issued by Let's Encrypt E7/E8). Without this step the zone runs on whatever the prior mode was; Full strict is the correct target for proxied origins with valid public certs.

## Certificate Renewal

Caddy renews Let's Encrypt certificates automatically using HTTP-01 challenge. With CF orange-cloud active, the ACME CA hits port 80 on CF's anycast IP; CF proxies the `/.well-known/acme-challenge/` request to the VM. Per CF documentation, "Always Use HTTPS" does not block HTTP DCV, and CF bypasses HTTPS redirects for `/.well-known/*` during validation. No Caddyfile change is needed.

Fallback if HTTP-01 proves unreliable: switch to DNS-01 via the `caddy-dns/cloudflare` Caddy module. The existing `CLOUDFLARE_API_TOKEN` has DNS Edit scope, which is sufficient for DNS-01 TXT record management. No new token needed.

## Verification Results (2026-06-12)

- DNS resolves to Cloudflare anycast (172.64.80.1), not the VM.
- `GET /health` returns HTTP 200 on both hostnames through CF edge.
- TLS at CF edge: Google Trust Services cert for scubed.co (CF Universal SSL active).
- SSE streaming: chat completion stream chunks deliver correctly through proxy (`data:` lines plus `[DONE]`).
- Origin: unaffected, Caddy still serving correctly on VM.

## Rollback

To restore grey-cloud (direct origin exposure), PATCH both DNS records to `proxied: false` via the CF API using `CLOUDFLARE_API_TOKEN` from `.env`:

```
PATCH /zones/fbe80ca310492206988c6fa6d5eb0622/dns_records/<id>
{"proxied": false}
```

Record IDs: api-hive = `70cee06aa48087837563d12f0617c388`, cp-hive = `42db291d707ca4378f7c8efdc14ad773`.
