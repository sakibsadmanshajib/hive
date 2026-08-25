# Cowork as a composer mode, plus four home-surface defects (PR for #944)

Capture log for the visual proof posted on the pull request. Images live on the
permanent `visual-proof-assets` GitHub Release, posted with
`scripts/post-pr-visual-proof.sh`, because a `raw.githubusercontent.com` URL
pinned to this branch would 404 the moment the branch is deleted on merge.

## Substrate

Two halves, named deliberately, because "ran it locally" is not a substrate.

* **Frontend under test**: the branch's own build, produced by
  `docker build -f deploy/docker/Dockerfile.open-webui --target frontend`, the
  exact stage the deploy image uses, then extracted from the image. Not a `npm
  run dev` server and not the host's node.
* **Backend**: the live demo box, `https://chat-hive.scubed.co`, untouched.
  A local harness serves the built frontend and forwards every `/api` call to
  the box with the session attached. `before` captures come from the box's own
  deployed bundle; `after` captures come from this branch's bundle against the
  same backend, so the delta is the frontend change and nothing else.

The harness serves this branch's `index.html` for any client route rather than
falling through to the box. The first attempt did fall through, screenshotted
the box's deployed bundle, and would have reported an unfixed defect as fixed;
the `after` artifacts capture screenshot was retaken after that was corrected.

## Session

Minted with the audited flow only. No password was set, reset or rotated at any
point. The Open WebUI session token is a short-lived token minted inside the
`open-webui` container for an account that already exists, by the same
mechanism as `scripts/owui-mint-admin-token.py`, read-only against the database.
The Supabase side used `admin/generate_link` plus `/auth/v1/verify`, which is
`apps/web-console/tests/e2e/support/live-auth.mjs`'s mechanism.

No URL in any capture carries a credential in its query string, so nothing
needed redaction in the images. Account address `demo@hive-demo.invalid` is a
non-routable test account, not a person's address.

## Before, measured on the deployed box

Queried from the DOM, not read off a screenshot:

```
DOM  {"quickstart":"NULL","hvGreeting":"NULL","composerModeToggle":"NULL",
      "navRows":"projects,agents,knowledge,scheduled",
      "bucketHeaders":"Today,Previous 7 days","listHeading":"Recents"}
MODEL MENU  {"left":1124,"right":1636,"viewport":1600,"overflowPx":36}
ARTIFACTS   {"title":"NULL","firstTop":0}
```

* `before-10-home.png` - the chat home with no greeting and no quick-start
  chips. Both shipped in #1161 and were invisible on this account.
* `before-11-modelmenu.png` - the model menu's right edge 36px past the window.
* `before-12-artifacts.png` - `/artifacts`, no title, empty state flush to the
  top of the pane.

## After, same backend, this branch's frontend

```
DOM  {"quickstart":"present","hvGreeting":"Good evening, demo@hive-demo.invalid",
      "composerModeToggle":"chat",
      "navRows":"projects,artifacts,knowledge,scheduled",
      "bucketHeaders":"NONE","listHeading":"Chats and tasks"}
MODEL MENU  {"left":681,"right":1193,"viewport":1600,"overflowPx":-407}
ARTIFACTS   {"title":"Artifacts","firstTop":526}
```

* `after-10-home.png` - greeting, chips, and the `Chat | Cowork` control.
* `after-11-modelmenu.png` - the menu anchored by its right edge, inside the window.
* `after-12-artifacts.png` - `/artifacts` with a page title, empty state centred.

## The composer mode (#944)

```
CHAT MODE    {"mode":"chat","segments":"chat:true cowork:false","role":"radiogroup",
              "coworkRow":"absent","voiceMode":"present","dictate":"present"}
DRAFT        {"draftBefore":"Reply with exactly the word COWORKPROOF and nothing else.",
              "draftAfter":"Reply with exactly the word COWORKPROOF and nothing else.",
              "same":true}
COWORK MODE  {"mode":"cowork","segments":"chat:false cowork:true",
              "coworkRow":"Knowledge work | Runs in a sandbox. Progress appears in this conversation.",
              "voiceMode":"absent","dictate":"present","url":"/"}
```

* `after-20-composer-chat-mode.png` - Chat selected, no second row, voice mode present.
* `after-21-composer-cowork-mode.png` - Cowork selected, the second row welded
  inside the same composer container, voice mode gone, dictation kept, the
  draft intact through the toggle, and the URL still `/`. This is #944's first
  acceptance criterion.
* `after-22-run-started.png` / `after-23-run-settled.png` - sending in Cowork
  mode creates a conversation at `/c/<id>` and renders the run in the ordinary
  transcript: user turn, hairline rule, assistant turn in the serif at the
  reading measure, composer still live below with Cowork still selected.
* `after-24-run-in-one-list.png` - #944's second acceptance criterion. Sidebar
  probe on that conversation:

```
SIDEBAR {"heading":"Chats and tasks","buckets":0,
         "rows":["<the run>","Reply with exactly this and nothing else: a l", ...]}
```

One heading, zero date buckets, the run interleaved with ordinary chats.

## What the run's terminal state shows, and why

The captured run ends in a service refusal rendered in the transcript rather
than in a result, and that is an environment fact rather than a defect in this
branch. `deploy/docker/owui-patches/hive_agent_proxy.py` resolves the caller's
Supabase OAuth token server side through `get_system_oauth_token`, and there is
currently no live `oauth_session` row on the box for any account: the demo
account's row was present and working earlier in the same session (a live
`GET /v1/agent/tasks` returned a task list including a `succeeded` run whose
`result_summary_ref` carries the exact markdown this branch renders), and had
been removed by the time the run was submitted. An automated capture cannot
mint one without completing the interactive OAuth consent journey.

Worth a separate look: that expiry happened roughly an hour after sign-in,
which is the shape of issue #782, whose fix merged as #787. Not investigated
here and not claimed as a regression, only recorded.

The success and failure paths render through the same two functions
(`applyCoworkRun` and `renderRun`), and `renderRun` is unit tested against the
succeeded, failed, cancelled, unknown, queued and running wire shapes.

## Known cosmetic artifact of the harness

The brand mark at the sidebar's top left renders as a placeholder glyph in the
`after` captures. That is the harness not serving one static asset, not a change
in this branch; the `before` captures from the box show the real mark.
