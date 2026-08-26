# Cowork run progress: re-verification of PR #1209 against issue #1206

Date: 2026-08-26. Substrate: the deployed demo box (`hive-demo-cf`), commit
`8ce450a2` (PR #1209, confirmed the box's current running `control-plane`
image and confirmed via `docker exec hive-control-plane-1 ... git log -1`
resolving to that exact SHA). Real Apptainer sandbox via the socket arm
(`HIVE_AGENT_ENGINE_SOCKET=/run/hive-agent/engine.sock`), same as the prior
verification (`docs/proof/cowork-run-progress-live-2026-08-26/`). Same
account (`browser-slice-20260824@hive-e2e.invalid`), same instructions
("Run `ls -la` in the workspace... create notes.md..."), same capture script
byte-for-byte (`apps/web-console/tests/e2e/support/live-auth.mjs` /
`live-auth.ts` unmodified, and the standalone capture harness reused verbatim
from the prior pass), so this is a fair comparison to the baseline that
issue #1206 recorded.

**Two runs, not one.** The first run came back with a result flatly worse
than the baseline (2 events total, none of them real). Rather than accept
that as the final verdict on one data point, a second run followed
immediately to establish whether that was systematic or a one-off race.
It was a race, and the box's own logs prove which race.

## Run 1: task `5a5313ff-a93e-4c70-bcbf-b440d2cee9ac`, total void, worse than baseline

Wall time: created `02:49:20.804963`, updated `02:50:03.477442` (succeeded)
= **43 seconds**, genuinely faster than baseline's 82s.

`public.agent_task_events` for this task, queried directly against
`hive-supabase-db-1`, not inferred from the UI:

| seq | kind | payload | created_at |
|---|---|---|---|
| 1 | status | `{"status":"running"}` | 02:49:48.47 |
| 2 | status | `{"status":"succeeded"}` | 02:50:03.49 |

**Two rows. Total.** No `SystemPromptEvent` noise, no echoed prompt, no
`.git` listing, but also zero `tool_call`, zero `tool_result`, zero `file`.
The task genuinely ran `ls -la` and wrote `notes.md` (`agent_tasks.result_summary_ref`
confirms the real, correct answer), but not one event describing that work
reached the database.

`hive-control-plane-1`'s own log for this window carries the reason:

```
2026/08/26 02:50:03 agentengine: /events: status 502: controlclient: request failed:
  Get "http://agent-server.control/api/conversations/04494460-3416-4a1b-bf44-21860c390836/events/search?limit=100":
  dial unix /home/sakib/agent-runtime/sessions/3667032889/c/agent.sock: connect: no such file or directory
2026/08/26 02:50:03 WARN agenttask: event sync pull failed task_id=5a5313ff-a93e-4c70-bcbf-b440d2cee9ac error="agentengine: /events: status 502"
```

`finishVanished`'s pull (PR #1209's actual fix) fired at exactly the same
second the task's status flipped to `succeeded`, and it 502'd: the sandbox's
own per-conversation control socket was already gone by the time the "last
possible pass" ran. Not a slow response, not a timeout: the socket file did
not exist. The fix's mechanism for a fast task raced the sandbox's own
teardown and lost.

Screenshots (`06-inflight-t20s.png`, `06-inflight-t50s.png`): identical to
each other in substance, "Working on it." with zero step lines at t20s, and
already the finished answer with zero step lines *ever having appeared* by
t50s (the task had completed by t43s). Every intermediate checkpoint between
submit and settlement is either blank progress or the final answer; no
transition state carrying a step line exists anywhere in this run.

## Run 2: task `64983c00-bb2c-4917-a954-00b38f2359e7`, real tool events, mid-run, readable

Wall time: created `02:55:34.472428`, updated `02:56:48.597388` (succeeded)
= **74 seconds**.

`public.agent_task_events` for this task:

| seq | kind | payload (abridged) | created_at | elapsed from create |
|---|---|---|---|---|
| 1 | status | `{"status":"running"}` | 02:56:03.56 | 29s |
| 3 | tool_call | `terminal`, `tool_call_id=chatcmpl-tool-b4d3295dd99336ad`, preview "List all files in workspace" | 02:56:18.59 | 44s |
| 4 | tool_result | same `tool_call_id`, preview `ls -la ... total 8 ... .git ...` | 02:56:18.59 | 44s |
| 8 | tool_call | `file_editor`, `tool_call_id=call_0491ef292a0d469b8ff199d8`, preview "Create notes.md..." | 02:56:33.62 | 59s |
| 9 | tool_result | same `tool_call_id`, preview "File created successfully at: /workspace/notes.md" | 02:56:33.62 | 59s |
| 10 | file | `{"name":"notes.md","size":73,...}` | 02:56:33.62 | 59s |
| 11 | status | `{"status":"succeeded"}` | 02:56:48.63 | 74s |

Seq 2, 5, 6, 7 are absent from the result set entirely: consistent with
PR #1209's stated mapping-layer filter for `SystemPromptEvent`,
`ConversationStateUpdateEvent`, the echoed user prompt, and dot-prefixed
workspace entries. None of that bootstrap/bookkeeping noise reached the
table this time, and none of it needed the frontend's "cannot read"
fallback, because it was never sent.

**`tool_call` and `tool_result` both appear, and they pair correctly**:
seq 3/4 share `tool_call_id=chatcmpl-tool-b4d3295dd99336ad`; seq 8/9 share
`tool_call_id=call_0491ef292a0d469b8ff199d8`. This is the first time either
kind has been observed in `agent_task_events` across both verification
passes of this feature.

**Timeline: the real work landed mid-run, not just at the start or only at
the end.** The 44s and 59s marks sit well inside the 74s run, 15-30s before
completion. Screenshots at `06-inflight-t50s.png` (collapsed: "Used
terminal: ls -la [?2004ltotal 8...") and `06-inflight-t65s.png` (expanded:
"Used terminal: ls -la..." and "Used file_editor: create File created
successfully at: /workspace/notes.md..." both rendered as readable text with
real command output) confirm the DB rows actually reached the browser and
rendered, not merely landed server-side. Neither line reads "An update this
version of Hive cannot read." **The mid-run void from the baseline is gone
for this run**: the transcript visibly changes between t35s (nothing yet)
and t50s/t65s (real step content), which the baseline's six checkpoints
never once did.

Reload mid-run at t50s (`07-reloaded-t50s.png`, `08-reloaded-expanded-t50s.png`):
the collapsed step line survived the reload, same behavior the baseline
already established for stale rows and now also holding for real ones.

`09-final.png`: settles cleanly to the accurate final answer, ordinary
message controls present, full step history (`Used terminal:`, `Used
file_editor:`, workspace file line) still visible above it, no shimmer.

One rendering rough edge, not a blocker: the terminal preview text carries
raw ANSI escape sequences verbatim (`[?2004l`, visible as literal bracket
text in the line), and the "Workspace file: notes.md" line appears to render
twice around the file-editor step in the expanded view. Neither breaks
readability of the two lines that matter (what tool ran, what it did), but
neither is polished.

## What actually distinguishes the two runs

`hive-control-plane-1`'s log shows **the exact same 502 socket-teardown
error, at the exact same relative moment (task completion), in both runs**:

```
2026/08/26 02:56:48 agentengine: /events: status 502: controlclient: request failed:
  Get "http://agent-server.control/api/conversations/6cddf47c-095a-44fe-a051-0db88ae94d50/events/search?limit=100":
  dial unix /home/sakib/agent-runtime/sessions/1250681324/c/agent.sock: connect: no such file or directory
2026/08/26 02:56:48 WARN agenttask: event sync pull failed task_id=64983c00-bb2c-4917-a954-00b38f2359e7 error="agentengine: /events: status 502"
```

Grepping the full control-plane log for both windows end to end
(`control-plane-log-excerpt.txt`, this directory) turns up **only** these two
`WARN agenttask: event sync pull failed` lines, one per run, both at the
moment the task left the active set. There is no log evidence of
`finishVanished`'s own pull ever succeeding in either run: it 502'd 2 for 2.

So PR #1209's actual "last possible pass" mechanism did not save run 2
either. What saved run 2 was that its task ran long enough (74s, tool
activity at 44s/59s) for the **ordinary in-flight polling loop**
(`syncTask`, active-set polling, which produces no log line on success) to
catch the tool events *before* the task completed and vanished from the
active set. Run 1's task was fast enough (43s) that no such in-flight poll
appears to have landed with anything to report before completion, and the
one mechanism meant to catch exactly that case, the finish-time pull, is
dead on arrival against this box's socket-teardown timing: 2 for 2 failed
with the identical dead-socket error.

This means the fix, as shipped, does not reliably close the gap issue #1206
reported. It closes it *only when a task runs long enough for an ordinary
in-flight poll to land before completion*, which is closer to luck than to
what the PR description claims ("the tail is always synced on the last
possible pass"). A task fast enough to finish inside one polling interval,
which is exactly the shape of task the bug was filed against (`a98420c4`,
82s baseline; this run 1's 43s is faster still), reproduces the original
symptom in full: zero tool events, nothing but two synthetic status
bookends, no readable trace of what the sandbox actually did while it was
the only thing happening.

## Files

* `run2-void-console-log.txt` / `run2-void-network-log.txt`: run 1's own
  script log and its `/hive/agent/*` request trace (the "void" run; despite
  the filename prefix "run2", this is described above as **Run 1**, matching
  the order the two runs actually happened in).
* `run3-working-console-log.txt` / `run3-working-network-log.txt`: run 2's
  own script log and request trace (the run with real tool events; described
  above as **Run 2**).
* `control-plane-log-excerpt.txt`: every `agenttask`/`agentengine`/
  `eventsync` line `hive-control-plane-1` logged across both runs' full
  windows (02:49:00 to 02:57:50 UTC), grepped directly from `docker logs`.

Screenshots were posted to issue #1206 directly (via a manual replication of
`scripts/post-pr-visual-proof.sh`'s upload-to-release mechanism; that script
itself only targets pull requests via `gh pr view`/`gh pr comment` and #1206
is a closed issue, not a PR, so it could not be invoked as written without
modifying it, which is out of scope for a verification pass). They are not
duplicated as files here; this directory carries the text evidence
`npm run lint:proof-tokens` scans, per that check's actual scope.

Checked all four text log files above for `eyJ`, `access_token`,
`refresh_token`, `token=`, `code=`, `apikey`, `service_role`,
`sb_publishable`, `token_hash`, `bearer` before writing this directory: none
present.

No account password was read, set, or rotated. No shared/demo account was
used; both runs authenticated as the same dedicated e2e identity the prior
verification pass established.
