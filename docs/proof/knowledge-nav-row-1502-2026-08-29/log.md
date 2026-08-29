# Knowledge navigation row removed: capture log

Branch: `fix/1502-knowledge-nav-row`
Issue: #1502
PR: #1516
Date: 2026-08-29

## Substrate, stated plainly

`hive-owui:proof-1502`, built from this branch's tree with
`docker build -f deploy/docker/Dockerfile.open-webui .`, run standalone on the
development box against its own throwaway container storage. Not the demo box,
and not a compose stack. The container was deleted after the run.

That substrate is the right one for what is under test and the wrong one for
nothing here: the navigation rows come from `HIVE_NAV`, a static array in the
frontend bundle, rendered by `ShellNav.svelte` with no permission filter and no
backend call. Whether the row exists is decided entirely inside the image this
build produced.

This box holds no Supabase credentials (`SUPABASE_URL`, `SUPABASE_ANON_KEY`,
`SUPABASE_SERVICE_ROLE_KEY` and `SUPABASE_DB_URL` are all empty in `.env`), so a
session on the deployed box cannot be minted from here at all and no capture
below touches any deployed environment.

The account is local to the throwaway container, created through
`POST /api/v1/auths/signup`. Its password was generated at run time and is
deliberately not recorded here. No URL in this capture carries a credential in
its query string.

## What was captured

Signed in at `http://localhost:3099/`, viewport 1440x900, light theme.

### 1. The rendered navigation, read as data

```js
Array.from(document.querySelectorAll('[data-hive-nav]'))
  .map(a => a.getAttribute('data-hive-nav') + ' -> ' + a.getAttribute('href'))
```

printed:

```
[
  "projects -> /projects",
  "artifacts -> /artifacts",
  "skills -> /skills",
  "scheduled -> /schedules"
]
```

No `knowledge` row and no `/knowledge` href. Read from the DOM rather than from
the source, so it is the shipped bundle answering, not the file.

### 2. The screenshot

`i1502-01-sidebar-after.png`, posted to PR #1516 through
`scripts/post-pr-visual-proof.sh`. The sidebar reads, in order: New Chat,
Search, Projects, Artifacts, Skills, Scheduled, then Folders and the
conversation list. The issue reported that same list with `Knowledge` sitting
between Artifacts and Skills.

The conversation visible in that sidebar ("Cowork run: sixcap.txt") is the
seeded fixture for the separate issue #1509 capture, which shares this
container. It is not part of what this change touches.

## What this capture does NOT show

- The `/knowledge` route itself. It is deliberately still there and still
  answers; only the row pointing at it is gone. See the pull request body for
  why that is stated rather than decided here.
- Single sign-on, the Caddy front end and the gateway hop, none of which exist
  in a standalone container. None of them can add a navigation row.

## Tests

`scripts/test-owui-hive-frontend.sh`: 20 files, 264 tests passed. The same
sources ran again inside the image build (`npm run test:frontend -- --run`,
stage `frontend 8/9`), which printed `Test Files 20 passed (20)` and
`Tests 264 passed (264)` and would have failed the build otherwise.
