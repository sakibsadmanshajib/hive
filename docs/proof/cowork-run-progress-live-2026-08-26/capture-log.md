# Cowork run progress: live capture, real sandbox

Date: 2026-08-26
Substrate: the deployed demo box (`hive-demo-cf`), commit `bf8edcb6a` (#1203, the
box's current HEAD, which includes #1193 and #1202). Real Apptainer sandbox via
the socket arm (`HIVE_AGENT_ENGINE_SOCKET=/run/hive-agent/engine.sock`, bind
mounted from the host launcher at `/home/sakib/agent-runtime`). No stub, no
mock. This closes the gap #1202's own capture log disclosed: that capture used
a local `agent_stub.py` because the demo box did not yet carry #1193, no real
sandbox could run on the WSL2 dev box, and no live session could be minted. All
three blockers are gone as of this run.

## Account

`browser-slice-20260824@hive-e2e.invalid`, a dedicated non-demo e2e identity
already present on the box (created 2026-08-24 by an earlier verification
pass). Its tenant has no explicit `ENABLE_COWORK` row, so it reads the
registry default (`feature_gate_keys.default_enabled = true`). Per
`docs/live-test-auth.md`, a suite that submits a real task must not run against
the shared demo account; this run submitted one real task, so it ran as this
dedicated identity, not `demo@hive-demo.invalid`.

## Auth: two real, box-specific facts, recorded so the next run does not re-lose the time

1. The admin one-time-token mint (`POST /auth/v1/admin/generate_link`) has to
   hit the **internal** Supabase gateway, `http://caddy-supabase` (reachable
   only from inside the `hive_default` docker network). The **public**
   `console-hive.scubed.co` also proxies `/auth/v1/*`
   (`deploy/docker/Caddyfile.console`), but it forwards to `caddy-supabase`'s
   **public** listener on port 8080, whose `@admin` matcher in
   `deploy/docker/Caddyfile.supabase` refuses `/auth/v1/admin/*` with a flat
   404. `live-auth.mjs`'s default (`SUPABASE_URL`) has to be the internal
   address for the mint to succeed at all.
2. The cookie the browser needs is a different matter: `@supabase/ssr` derives
   the cookie name from the URL and anon key passed to `createBrowserClient`,
   and the deployed web-console container is built with
   `NEXT_PUBLIC_SUPABASE_URL=https://console-hive.scubed.co` and
   `NEXT_PUBLIC_SUPABASE_ANON_KEY=sb_publishable_...` (the new publishable-key
   format, not the legacy JWT anon key). Minting cookies with the internal URL
   and the JWT anon key produces a session console-hive's own client does not
   recognise: the browser lands back on `/auth/sign-in`, no error, no crash,
   just silently signed out. The fix is to mint (admin call) against the
   internal gateway but build the cookies (`sessionCookies()`) against the
   **public** console URL and the **publishable** anon key, i.e. the exact
   pair the deployed app itself uses. Once that split was made, the console
   session, and the `Continue with Hive` OIDC hop into chat, both worked on
   the first try, with no visible consent screen (already granted).

No account password was read, set or rotated. `apps/web-console/tests/e2e/support/live-auth.mjs`
and `.../live-auth.ts` (`signInToChat`) were used byte-for-byte as committed on
`origin/main`; nothing in the sanctioned mint path was modified.

## What the run showed

Task `a98420c4-95a9-48cd-af03-3bea58e42241`, pack `knowledge-work-pack`,
instructions: "Run `ls -la` in the workspace and tell me exactly what files are
present. Then create a file named notes.md containing one sentence summarizing
what you found." Verified directly against `public.agent_tasks` /
`public.agent_task_events` on the box's own Postgres, not only through the UI.

* **The `Chat | Cowork` toggle renders and switching preserves the draft.**
  A draft typed in Chat mode ("draft written before switching to Cowork mode,
  should survive the toggle") was still in the composer, verbatim, after
  clicking the Cowork segment. `radiogroup[data-hive-composer-mode]` correctly
  reported `chat` before the click and `cowork` after.
* **Submitting in Cowork mode launches a real sandbox task, confirmed off the
  box, not just the UI.** `agent_tasks` carries the row with `status=succeeded`
  and a `result_summary_ref` that is an accurate description of what actually
  ran: real `ls -la` output (`.`, `..`, `.git`, no other files) and a real
  `notes.md` written with that summary. Wall time: 82 seconds
  (created 01:42:49.959, updated 01:44:12.336). This is genuine OpenHands
  sandbox output, not something a scripted answer could fabricate correctly
  (the workspace really did contain only `.git`, and the summary said so).
* **Per-step lines DO NOT render while the run is in flight, for the whole
  substantive part of the run.** This is the actual finding, and it is a
  negative one. `agent_task_events` holds exactly 7 rows for this task:

  | seq | kind | what it is |
  |---|---|---|
  | 1 | status | `{"status":"running"}` — correctly deduped by the frontend (no line) |
  | 2 | status | a raw `SystemPromptEvent` (the OpenHands SDK's own tool-list/system-prompt envelope), with neither `sandbox_kind` nor a `status` field, so it falls to the frontend's last resort and renders as **"An update this version of Hive cannot read."** |
  | 3 | message | the user's own submitted instructions, echoed back by the engine's own event feed and rendered verbatim as a step line, duplicating the prompt already shown above the transcript |
  | 4 | status | a `ConversationStateUpdateEvent` (`key: last_user_message_id`) — same shape gap as #2, same fallback text |
  | 5 | status | a second `ConversationStateUpdateEvent` (`key: execution_status`) — same fallback text again, a third time |
  | 6 | file | `{"name": ".git", "size": 4096, ...}` — a workspace-listing artifact from sandbox bootstrap, rendered as `Workspace file: .git` |
  | 7 | status | `{"status":"succeeded"}` — correctly deduped |

  Every one of rows 1-6 landed within the same four seconds (all timestamped
  `01:43:12`, close to task creation). Then **nothing**: the browser's own
  request log shows the follower polling `after_seq=6` every ~3.2 seconds for
  a full 58 seconds straight (`01:43:17` through `01:44:15`) with zero new
  events, while the sandbox was genuinely running `ls -la` and writing
  `notes.md` inside it. The turn sat on "Working on it." with the same six
  stale lines the entire time, then jumped straight to the finished answer.
  Screenshots at t20s, t35s, t50s, t65s, t80s and t100s are visually
  indistinguishable in the transcript area (`06-inflight-t*.png`), which is
  the same evidence from a different angle: nothing changed on screen for the
  whole real-work window.
* **The event kinds that actually appeared do not match what #1202 built
  against.** #1073's `mapSandboxEvent` was believed to translate OpenHands
  `ActionEvent` / `ObservationEvent` pairs (a bash call, a file edit) into
  `tool_call` / `tool_result`, which is what #1202's stub simulated
  (`execute_bash`, `str_replace_editor`, joined on `tool_call_id`). This run's
  agent had `TerminalTool` and `FileEditorTool` available (both listed in the
  `SystemPromptEvent` payload), and it used them for real: it ran `ls -la` and
  it wrote `notes.md`, both stated correctly in the final summary. Neither
  action produced a `tool_call`, a `tool_result`, or any event describing them
  at all in `agent_task_events`. **Zero `tool_call`, zero `tool_result`, zero
  `error` events appeared in this run.** The only kinds seen live were
  `status`, `message` and `file`, and three of those five non-deduped rows
  render as either dead text ("An update this version of Hive cannot read.",
  three times) or noise unrelated to the user's request (the user's own prompt
  echoed back, a `.git` directory listing). This is the real gap: either
  agent-engine is not yet emitting `tool_call`/`tool_result` for ordinary
  terminal and file-editor actions on this build, or it emits something
  `eventsync.go`'s `mapSandboxEvent` does not yet recognise and folds into a
  bare `status` row instead. Establishing which of those two it is needs a
  read of the running agent-engine process's own OpenHands-side log, which
  this pass did not have a location for (`/home/sakib/agent-runtime` carries
  no discoverable log file or systemd unit); that is follow-up, not something
  this capture can settle on its own.
* **The run settles correctly.** `09-final.png`: the transcript shows the
  accurate final answer, no shimmering dot, ordinary message controls
  (edit/copy/speak/thumbs/retry) present, composer back to idle. No
  regression there.
* **Reload mid-run: the follower resumed from its cursor, not from zero, and
  every line survived.** The page was reloaded at t35s
  (`07-reloaded-t35s.png`). The six lines already shown were still shown
  afterward; the very next events request carried `after_seq=6`, the same
  cursor the pre-reload page already held, not `after_seq=0`. `latestStepSeq`
  is doing what its comment says.
* **The composer reset to Chat mode after reload**, confirmed
  (`data-hive-composer-mode` read `chat` immediately post-reload,
  `07-reloaded-t35s.png` shows the `Chat` segment selected with no Cowork row).
  Per the task brief this is known #1193 behaviour, not a regression. In
  practice, mid-run, it reads as faintly wrong: the user is still looking at a
  running Cowork turn, but the control in front of them now claims their next
  message would be an ordinary chat message. It does not corrupt anything
  (the run keeps going and the turn keeps updating regardless of composer
  mode), but it is a rough edge worth a follow-up look, not a blocker on its
  own.
* **An unrelated observation, not scoped to fix here**: this account's
  transcript carries a persistent "You're out of credits." banner in every
  screenshot, left over from this identity's own prior use. The Cowork
  submission and the sandbox run were unaffected by it (the task still ran to
  completion), which is itself worth a note: either credit balance does not
  gate a Cowork submission, or this account still has some other allowance
  Cowork draws from. Out of scope for this capture; flagged for whoever looks
  at Cowork billing gates next.

## Substrate detail, stated the way the earlier capture stated its stub

* `HIVE_AGENT_ENGINE_SOCKET=/run/hive-agent/engine.sock` inside
  `hive-control-plane-1`, bind-mounted from `/home/sakib/agent-runtime/run` on
  the host, where the host launcher (`agent-engine -serve
  .../engine.sock`) was already running. `docker logs hive-control-plane-1`
  carries `agent-engine daemon reachable at /run/hive-agent/engine.sock` from
  this box's own most recent boot, i.e. the socket arm, which per this repo's
  `CLAUDE.md` is the only arm any shipped deployment runs.
* Browser: a `mcr.microsoft.com/playwright:v1.62.0-jammy` container (matching
  the `@playwright/test@1.62.0` resolved in `apps/web-console`'s own
  `node_modules`, mounted read-only), attached to the `hive_default` docker
  network so the mint's internal call could reach `caddy-supabase`, with
  ordinary outbound internet access for the real public
  `https://console-hive.scubed.co` / `https://chat-hive.scubed.co` hops.
* `public.agent_tasks` / `public.agent_task_events` queried directly against
  `hive-supabase-db-1` on the box, independent of anything the UI rendered.

## Files

* `00-chat-loaded.png` — chat home, signed in, `Chat | Cowork` toggle visible.
* `02-chat-mode-with-draft.png` — draft typed in Chat mode.
* `03-cowork-mode-draft-check.png` — same draft, intact, after switching to
  Cowork; the Cowork row ("Knowledge work / Runs in a sandbox. Progress
  appears in this conversation.") visible underneath.
* `05-submitted-t0.png` — the real instructions just after submit.
* `06-inflight-t35s.png` — expanded step list, ~35s in: the six stale lines
  described above, unchanged for the rest of the run.
* `07-reloaded-t35s.png` — immediately post-reload: lines survived, composer
  back to `Chat`.
* `09-final.png` — settled, accurate final answer, no shimmer.

`console-log.txt` and `network-log.txt` (this capture's own run log and its
full `/hive/agent/*` request trace) are attached as PR comment text rather than
committed images; neither carries a credential (checked for `eyJ`,
`access_token`, `refresh_token`, `apikey`, `service_role`, `sb_publishable`,
`token_hash`, `bearer` before writing this log — none present).
