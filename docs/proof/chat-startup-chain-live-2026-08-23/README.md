# Proof: the chat startup chain, re-measured on a live full stack

Supplements the 2026-08-17 measurement already in
`docs/proof/chat-first-paint-2026-08-17/`. That one is not withdrawn: it is a
larger sample, paired and interleaved, against the real deployment, and it
remains the better evidence for the wall-clock size of the win.

This one exists for two reasons the older one cannot cover.

1. **It postdates #1007**, which restructured the customer-facing model
   catalog. `/api/models` is the single heaviest request in the old waterfall
   (818 ms there), so a catalog change is exactly the kind of thing that could
   have invalidated the earlier numbers. It did not.
2. **It is a whole stack, not an interception harness.** The older arm served
   a locally built bundle in place of the deployed one while every API call
   went to the live deployment. Here every component is real and local, so
   there is no hybrid to reason about.

## Substrate

One machine, back to back, nothing else changed between arms:

- self-hosted Supabase data plane (`selfhost` profile): Postgres, GoTrue
  v2.189.0, PostgREST, storage, `caddy-supabase`
- `control-plane`, `edge-api`, `litellm`, `redis`
- `web-console-prod` behind `caddy-console`, serving `/auth/v1`
- Open WebUI behind `caddy-owui`

Both arms are signed in as the same account through a real OAuth 2.1
authorization-code round trip. No session was injected.

- **BEFORE** arm: `main` merged with #952 (which merges first).
- **AFTER** arm: the same, plus this branch.

`caddy-owui` was force-recreated rather than restarted between arms. The
Caddyfile is a single-file bind mount, and such a mount stays pinned to the
inode it was started with, so a restart would have kept serving the old config
and the header change would have silently not been under test.

## Result

| | Before | After |
|---|---|---|
| Serial dependency waves (5 runs) | 5, 5, 4, 4, 5 | 1, 2, 2, 2, 2 |
| Composer visible, upper median | 1071 ms | 901 ms |
| Composer visible, all samples | 1090, 1053, 1071, 1027, 1125 | 901, 853, 889, 1122, 1032 |
| API requests during startup | 20 | 19 |
| `/_app/immutable` `Cache-Control` | *(none)* | `public, max-age=31536000, immutable` |

A **wave** is a set of requests overlapping in time; a new wave begins only
when a request starts after everything before it has already finished. Waves,
not request count, are what a round-trip chain costs, because requests inside
one wave are paid for once.

The shape of the change is visible in the waterfalls in `before.log` and
`after.log`. Before, the first three waves are a ladder one request wide:
`/api/config`, then `/api/v1/auths/`, then `/api/config` again, and only then
does the real fan-out start. After, `/api/v1/auths/`, `/api/models` and
`/api/config` all begin together in the first wave.

## What this measurement does not claim

Every hop here is loopback, so a round trip costs tens of milliseconds instead
of the hundreds it costs through the tunnel. The wall-clock gain on this
substrate (about 170 ms at the median) therefore **understates** the deployed
one, and the 2026-08-17 measurement against the real deployment (3097 ms to
1916 ms upper median, after arm winning 6 of 6 pairs) is the honest figure for
what a user experiences.

Depth is the durable result: it is structural, does not move with machine
load, and reproduced on every run.

The `Cache-Control` line is asserted directly from the response headers rather
than inferred from a timing, and it is a returning-visitor benefit that this
measurement's fresh browser context deliberately does not exercise.
