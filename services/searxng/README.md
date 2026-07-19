# SearXNG (Hive self-host)

Backend for Hive's web-search tool. Consumed by:
- Open WebUI native web-search (per-chat toggle, verified+ users).
- Edge-API `/v1/tools/web_search` (OpenAI function-tool endpoint).

## Status

Scaffold only. Full Phase 26 implementation will land:
- `settings.yml` — engine allowlist, JSON output, instance metadata
- `ENGINE-AUDIT.md` — security-reviewer signoff on engine list
- Docker compose activation in `deploy/docker/docker-compose.yml`

Full plan tracked in the project vault (see roadmap board in the root README).

## Hardening (when activated)

- Bind to docker network only (no host port).
- `SEARXNG_SECRET` from env, rotated 90-day.
- Disable engines that scrape PII without consent.
- `safe_search` default: `moderate`.
- Edge-api rate-limits requests before SearXNG sees them.
