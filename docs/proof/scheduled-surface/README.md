# Scheduled surface proof (PR #1118)

Claim: the Scheduled nav row is reachable in the shell sidebar and routes to a
presentable `/schedules` empty state per the D-045 sidebar grammar.

Method: one throwaway Open WebUI container (`schedproof-owui`) built from this
branch's `deploy/docker/Dockerfile.open-webui` (image
`hive-open-webui:scheduled-proof`, built at commit 2f9aa197e), auth disabled,
offline mode, no provider keys. Captured with Playwright against
`http://127.0.0.1:8123`. The container and its network are removed after the
capture; `run.sh` in this directory reproduces it end to end.

- `01-sidebar-scheduled-row.png`: shell sidebar with the Scheduled row (clock
  glyph) between Knowledge and Folders, pointing at `/schedules`.
- `02-schedules-empty-state.png`: the `/schedules` destination. Panel header
  with a persistent New button, centered empty state (clock glyph, Scheduled
  title, "Templated runs kicked off on schedule." explainer, New button), and
  the honest not-bridged notice under it. Only the Scheduled sidebar row is
  active-highlighted.

No credentials appear in any URL on this flow (auth disabled throwaway
container, loopback hostnames), so there is nothing to redact in the log or
the pixels.
