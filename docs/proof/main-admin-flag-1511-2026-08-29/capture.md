# main.py admin-bypass flag gate: capture log, 2026-08-29

> **Read the dated sections in order.** Everything above the
> "Security review round, 2026-08-30" heading is the record as it stood on
> 2026-08-29, when the patch rewrote five sites and the marker count was **5**.
> The review round that follows repairs a channel-arm regression, which adds a
> helper and one arm per endpoint and moves the count to **8**. The earlier
> transcripts are left verbatim rather than retro-edited, because they are the
> evidence for what was observed at the time, but every "5 markers" line below
> this banner is superseded by the 2026-08-30 section. The current, shipping
> numbers are the ones in that section.

Issue #1511, third sibling from the security review of PR #1496 after #1474
(merged) and #1508. Branch `fix/1511-task-endpoints-admin-flag`.

## The severity correction, which matters more than the fix

The issue as I filed it said "on this instance every tenant OWNER holds an
administrator session (#748, #948)". **That is false on this deployment**, and I
should have checked it rather than carrying it over from the review comment.

`deploy/docker/owui-patches/tenant_role_from_db.py` resolves this login's Open
WebUI role from Hive's own Postgres. Its explicit purpose is that it no longer
maps a tenant OWNER onto `admin`: a login gets `admin` only when it owns an
account with `accounts.is_platform_admin = true`, the same predicate the control
plane uses for its own platform-admin surfaces. An ordinary tenant OWNER
resolves to `user`, and an email this Postgres has never seen is left at
`DEFAULT_USER_ROLE`, which is `pending` here. The patch's own header records the
live audit that forced that change, where a legitimately provisioned tenant
OWNER had received admin and read another tenant's chat titles and uploaded file.

**So the reachable class is Hive platform staff, not customers.** That is a much
smaller and already-trusted set. This is why it ships behind #1474 and #1508
rather than beside them, and why it is not urgent.

It is still worth closing, for one specific reason. Setting
`ENABLE_ADMIN_CHAT_ACCESS` to `"false"` is this deployment's statement that not
even a platform admin gets cross-tenant chat access through the product surface.
These five sites ignore that statement, so the flag does not mean what the
compose file says it means, and the next person reasoning from it will be wrong
in the direction that favours access.

## What is wrong, precisely

Not a pre-authorisation side effect. Both task endpoints resolve the chat before
acting, so unlike #1474 and #1508 the ORDER is already correct. What is wrong is
the WIDTH of the predicate: a bare `user.role != 'admin'` with no flag term,
which is exactly the shape `apply_router_authz_family_patch.py` closes for issue
#1186 on every router. `main.py` was never in that patch's file set, whose
`EXPECTED_MARKERS` covers eleven modules under `routers/` and no top-level
module, so these sites were not deliberately excluded, they were never looked at.

Five sites, all chat-ownership decisions:

| Site | What the admin term admits |
| --- | --- |
| `GET /api/tasks/chat/{chat_id}`, socket-scoped arm | read |
| `GET /api/tasks/chat/{chat_id}`, chat arm | read of another tenant's task ids, which are the ids `POST /api/tasks/stop/{task_id}` consumes |
| `POST /api/tasks/chat/{chat_id}/stop`, socket-scoped arm | cancellation |
| `POST /api/tasks/chat/{chat_id}/stop`, chat arm | the cancellation boundary #1474 closed on DELETE, through a different verb |
| chat-completions existing-chat ownership check | **write** into another user's conversation |

The fifth is beyond issue #1511 as filed and is included deliberately: same
predicate, same file, same flag, same patch, same revert unit, same caller, and
it is the most consequential of the five. Shipping four and leaving that one
would repeat the "patched only the path the ticket names" error this review
round already caught once on #1474's siblings.

Four other `user.role != 'admin'` occurrences remain in `main.py` and are left
alone. Named individually, because an earlier draft of this log described them
as "two feature permissions" plus the other two, and one of the four is not a
feature permission at all:

| Line | What it guards | Why it stays |
| --- | --- | --- |
| 1032 | model access, under `BYPASS_MODEL_ACCESS_CONTROL` | not a chat-ownership decision, and it has its own flag |
| 1121 | the `features.direct_tool_servers` permission on `tool_servers` | a feature permission, mirrors the storage-side strip |
| 1179 | **channel membership** on a `channel:` chat id | see below |
| 2201 | a public active-users count, under `ENABLE_PUBLIC_ACTIVE_USERS_COUNT` | not a chat-ownership decision, and it has its own flag |

Line 1179 is the one worth stating plainly rather than filing under "feature
permission". It is an administrator bypass of `Channels.is_user_channel_member`
on a group or direct-message channel, which is an access-control decision and
not a permission toggle. It is left out on scope rather than on principle:
`ENABLE_ADMIN_CHAT_ACCESS` is this deployment's statement about cross-tenant
CHAT access, channel moderation is a distinct and legitimately administrative
function, and folding it in would widen the blast radius of a patch whose whole
argument is that it reuses one existing predicate unchanged. Whether channel
membership deserves the same treatment is a real question and it is a separate
one; it is recorded here rather than quietly absorbed or quietly dropped.

The drift guard pins the surviving count at 4, so a newly introduced bare bypass
fails the build too.

## 1. Pre-fix: the defect, reproduced

```
pre-fix source: main.py WITHOUT the #1511 patch
  ok: the pre-fix source carries no # hive (#1511) marker
  ok: PRE-FIX: a non-owner administrator was handed the victim's task ids with ENABLE_ADMIN_CHAT_ACCESS off (observed ['task-for-e85bb8ac-32f1-4bcb-a5af-2c56060ce571'])
  ok: PRE-FIX: and cancelled the victim's tasks (refused=None, cancelled=['e85bb8ac-32f1-4bcb-a5af-2c56060ce571'])
  ok: with ENABLE_ADMIN_CHAT_ACCESS ON the administrator is allowed again, so the flag is what decides (listed=['task-for-e85bb8ac-32f1-4bcb-a5af-2c56060ce571'], refused=None, cancelled=['e85bb8ac-32f1-4bcb-a5af-2c56060ce571'])
  ok: the owner still lists and stops their own tasks (listed=['task-for-e85bb8ac-32f1-4bcb-a5af-2c56060ce571'], refused=None)
  ok: an ordinary non-owner is refused on both endpoints (listed=[], refused=404)
```

## 2. Post-fix

```
post-fix source: with the #1511 patch
  ok: the #1511 patch applied: 5 # hive (#1511) markers (found 5)
  ok: a non-owner administrator is handed NO task ids while the flag is off (observed [])
  ok: and cancels nothing, refused with 404 (refused=404, cancelled=[])
  ok: with ENABLE_ADMIN_CHAT_ACCESS ON the administrator is allowed again, so the flag is what decides (listed=['task-for-e85bb8ac-32f1-4bcb-a5af-2c56060ce571'], refused=None, cancelled=['e85bb8ac-32f1-4bcb-a5af-2c56060ce571'])
  ok: the owner still lists and stops their own tasks (listed=['task-for-e85bb8ac-32f1-4bcb-a5af-2c56060ce571'], refused=None)
  ok: an ordinary non-owner is refused on both endpoints (listed=[], refused=404)

the chat-completions ownership check (structural only)
  ok: the completions-path ownership check no longer carries a bare admin term
  ok: and carries the flag-gated one instead

PASS
```

The flag-ON leg is the one that distinguishes a fix from a lockout: with
`ENABLE_ADMIN_CHAT_ACCESS` set, the same administrator is allowed again in both
legs, so the flag is genuinely what decides. The owner and the ordinary
non-owner behave identically before and after.

## 3. The test can go red

Patch replaced with a no-op that prints and returns:

```
post-fix source: with the #1511 patch
  FAIL: the #1511 patch applied: 5 # hive (#1511) markers (found 0)
  FAIL: a non-owner administrator is handed NO task ids while the flag is off (observed ['task-for-e85bb8ac-32f1-4bcb-a5af-2c56060ce571'])
  FAIL: and cancels nothing, refused with 404 (refused=None, cancelled=['e85bb8ac-32f1-4bcb-a5af-2c56060ce571'])
  ok: with ENABLE_ADMIN_CHAT_ACCESS ON the administrator is allowed again, so the flag is what decides (listed=['task-for-e85bb8ac-32f1-4bcb-a5af-2c56060ce571'], refused=None, cancelled=['e85bb8ac-32f1-4bcb-a5af-2c56060ce571'])
  ok: the owner still lists and stops their own tasks (listed=['task-for-e85bb8ac-32f1-4bcb-a5af-2c56060ce571'], refused=None)
  ok: an ordinary non-owner is refused on both endpoints (listed=[], refused=404)

the chat-completions ownership check (structural only)
  FAIL: the completions-path ownership check no longer carries a bare admin term
  FAIL: and carries the flag-gated one instead

FAILED: 5 check(s)
  - the #1511 patch applied: 5 # hive (#1511) markers (found 0)
  - a non-owner administrator is handed NO task ids while the flag is off (observed ['task-for-e85bb8ac-32f1-4bcb-a5af-2c56060ce571'])
  - and cancels nothing, refused with 404 (refused=None, cancelled=['e85bb8ac-32f1-4bcb-a5af-2c56060ce571'])
  - the completions-path ownership check no longer carries a bare admin term
  - and carries the flag-gated one instead
```

## 4. The patch applies to the pinned backend image

```
apply_main_admin_flag_1511_patch: flag-gated 5 bare admin bypasses on chat ownership in main.py (#1511)
apply_main_admin_flag_1511_patch: already applied
markers: 5
```

with the emitted endpoint read back out of that container:

```
async def list_tasks_by_chat_id_endpoint(request: Request, chat_id: str, user=Depends(get_verified_user)):
    if chat_id.startswith('local:') or chat_id.startswith('channel:'):
        socket_id = chat_id[len('local:') :]
        owner_id = get_user_id_from_session_pool(socket_id)
        # hive (#1511): bare admin role is not enough; the same flag the
        # #1186 family patch uses for chats.py, which compose sets false
        if owner_id != user.id and not (user.role == 'admin' and ENABLE_ADMIN_CHAT_ACCESS):
            return {'task_ids': []}
    else:
        chat = await Chats.get_chat_by_id(chat_id)
        # hive (#1511)
        if chat is None or (
            chat.user_id != user.id and not (user.role == 'admin' and ENABLE_ADMIN_CHAT_ACCESS)
        ):
            return {'task_ids': []}
```

## 5. The Dockerfile drift guard goes red if the fix is dropped

```
--- guard with patch SKIPPED (must fail) ---
guard correctly FAILED
--- with the patch applied ---
DRIFT GUARD LINES PASS
```

Both halves: five `# hive (#1511)` markers, and exactly four surviving
`user.role != 'admin'` occurrences, so both a dropped fix and a newly introduced
bare bypass fail the build.

## What is covered structurally rather than behaviourally

The fifth site, the chat-completions ownership check, is not driven by the test.
It sits deep inside a several-hundred-line handler that cannot be lifted out and
executed against stubs the way the two task endpoints can. It is asserted
structurally instead, in both directions (the bare form absent, the flag-gated
form present), plus the patch's own AST postcondition. Saying so rather than
letting the behavioural coverage be read as extending to it.

## What could not be captured

No screenshot of the chat interface, and no live before-and-after against the
deployed box. Same three reasons as #1474 and #1508: nothing visible changes,
the after state does not exist until `deploy-demo-box.yml` next rebuilds the
chat image, and no live session could be minted because this checkout has no
`SUPABASE_URL`, `SUPABASE_SERVICE_ROLE_KEY` or `SUPABASE_ANON_KEY`. No password
was read, set, reset or rotated, and `demo@hive-demo.invalid` was not touched.

## Credentials

No URL in this capture carries a credential, and no token, key, password or
session appears in any line above. Nothing required redaction and nothing was
redacted, stated so the absence of a redaction note is not read as an oversight.

---

## Re-verification, 2026-08-30

This branch was adopted after its original author was lost to a session rate
limit. Everything above was treated as an unverified draft and re-run rather
than trusted. The branch was also rebased onto `origin/main` at `9f510c5d3`.

Every claim below was re-measured from scratch. Nothing here is carried over.

> **Superseded in part.** This section is the adoption re-verification, taken
> before the security review round that follows it. Its transcripts still show
> **5 markers**, which was correct at the time. The review round then repaired
> the channel-arm regression, which adds a helper plus one arm per endpoint and
> moves the count to **8**. Everything else in this section still holds: the
> reachability correction, the flag-off and flag-on legs, the owner and
> non-owner cases, and the drift guard being non-vacuous. Only the marker count
> and the emitted-code listing below it are out of date, and the 2026-08-30
> security review section carries the current ones.

### The suite still reproduces and still passes

Run in Docker (`python:3.12-slim`), against the working tree:

```
main.py chat task endpoints honour ENABLE_ADMIN_CHAT_ACCESS (issue #1511)
  ok: the vendored tree and the pinned backend image are the same open-webui version (vendor=0.10.2, pinned=0.10.2)
  ok: docker-compose.yml still sets ENABLE_ADMIN_CHAT_ACCESS false, so the flag-off leg below is the deployed configuration and not a hypothetical

pre-fix source: main.py WITHOUT the #1511 patch
  ok: PRE-FIX: a non-owner administrator was handed the victim's task ids with ENABLE_ADMIN_CHAT_ACCESS off (observed ['task-for-e85bb8ac-32f1-4bcb-a5af-2c56060ce571'])
  ok: PRE-FIX: and cancelled the victim's tasks (refused=None, cancelled=['e85bb8ac-32f1-4bcb-a5af-2c56060ce571'])

post-fix source: with the #1511 patch
  ok: a non-owner administrator is handed NO task ids while the flag is off (observed [])
  ok: and cancels nothing, refused with 404 (refused=404, cancelled=[])
  ok: with ENABLE_ADMIN_CHAT_ACCESS ON the administrator is allowed again, so the flag is what decides (listed=['task-for-e85bb8ac-32f1-4bcb-a5af-2c56060ce571'], refused=None, cancelled=['e85bb8ac-32f1-4bcb-a5af-2c56060ce571'])
  ok: the owner still lists and stops their own tasks (listed=['task-for-e85bb8ac-32f1-4bcb-a5af-2c56060ce571'], refused=None)
  ok: an ordinary non-owner is refused on both endpoints (listed=[], refused=404)

PASS
```

### The reachability correction is independently confirmed

Both halves checked against the shipped files rather than taken from the issue.

`deploy/docker/docker-compose.yml:1182` sets `ENABLE_ADMIN_CHAT_ACCESS: "false"`.

`deploy/docker/owui-patches/tenant_role_from_db.py:98-117` grants Open WebUI
`admin` only when the login owns an account with `a.is_platform_admin = true`,
alongside `m.role = 'owner'` and `m.status = 'active'`. An ordinary tenant OWNER
therefore resolves to `user`. The issue's original claim that every tenant OWNER
holds an administrator session is false on this deployment, and the corrected
severity in the pull request body is the accurate one: **the reachable class is
Hive platform staff, not customers.**

### The Dockerfile drift guard is not vacuous

This is the check that failed on the sibling branch #1523, where the pattern was
anchored at the wrong indentation and returned the same number before and after
the patch. Measured here inside the pinned image
`ghcr.io/open-webui/open-webui@sha256:9fcea9c6e32ab60b0498f3986c6cdf651ddbe61db48d2213a3d28048ddd673d4`,
both halves genuinely move:

```
=== image identity ===
	"version": "0.10.2",
=== BEFORE: drift guard on the unpatched image (must FAIL) ===
guard correctly FAILED before the patch
  markers before: 0
  bare admin checks before: 9
=== apply ===
apply_main_admin_flag_1511_patch: flag-gated 5 bare admin bypasses on chat ownership in main.py (#1511)
apply_main_admin_flag_1511_patch: already applied
=== AFTER: drift guard on the patched image (must PASS) ===
DRIFT GUARD LINES PASS
  markers after: 5
  bare admin checks after: 4
```

Zero to five markers, and nine to four surviving bare admin checks. Both are
conditions that can be false, which is the property the sibling branch lacked.

### The five sites, read back out of that container

```
1338:                    # hive (#1511): a write into someone else's chat, so the
1340-                    if not await Chats.is_chat_owner(chat_id, user.id) and not (user.role == 'admin' and ENABLE_ADMIN_CHAT_ACCESS):
1791:        if owner_id != user.id and not (user.role == 'admin' and ENABLE_ADMIN_CHAT_ACCESS):
1797:            chat.user_id != user.id and not (user.role == 'admin' and ENABLE_ADMIN_CHAT_ACCESS)
1814:        if owner_id != user.id and not (user.role == 'admin' and ENABLE_ADMIN_CHAT_ACCESS):
1820:            chat.user_id != user.id and not (user.role == 'admin' and ENABLE_ADMIN_CHAT_ACCESS)
```

The patch is idempotent: applied twice, the second run prints
`already applied` and changes nothing.

### Two mutations, both caught

**The patch replaced by a no-op.** Five red, including both behavioural
assertions and both structural ones for the fifth site:

```
FAILED: 5 check(s)
  - the #1511 patch applied: 5 # hive (#1511) markers (found 0)
  - a non-owner administrator is handed NO task ids while the flag is off (observed ['task-for-e85bb8ac-32f1-4bcb-a5af-2c56060ce571'])
  - and cancels nothing, refused with 404 (refused=None, cancelled=['e85bb8ac-32f1-4bcb-a5af-2c56060ce571'])
  - the completions-path ownership check no longer carries a bare admin term
  - and carries the flag-gated one instead
```

**A lockout wearing the fix's clothing.** The more interesting one, because a
predicate that refuses the administrator unconditionally satisfies every
"nothing leaked" assertion. `FLAGGED` mutated to
`(user.role == 'admin' and ENABLE_ADMIN_CHAT_ACCESS and False)`, which still
carries `ENABLE_ADMIN_CHAT_ACCESS` as a conjunct and so still satisfies the
patch's own AST postcondition:

```
post-fix source: with the #1511 patch
  ok: the #1511 patch applied: 5 # hive (#1511) markers (found 5)
  ok: a non-owner administrator is handed NO task ids while the flag is off (observed [])
  ok: and cancels nothing, refused with 404 (refused=404, cancelled=[])
  FAIL: with ENABLE_ADMIN_CHAT_ACCESS ON the administrator is allowed again, so the flag is what decides (listed=[], refused=404, cancelled=[])
```

The flag-ON leg is the only thing that separates this fix from a silent removal
of platform-admin access, and it goes red. That leg is load bearing and is not
decoration.

## Visual proof, restated

Unchanged and still true. Nothing visible changes, the after state does not
exist on the running stack until `deploy-demo-box.yml` next rebuilds the chat
image, and no live session could be minted from this checkout because it carries
no `SUPABASE_URL`, `SUPABASE_SERVICE_ROLE_KEY` or `SUPABASE_ANON_KEY`. No
password was read, set, reset or rotated, and `demo@hive-demo.invalid` was not
touched. Saying so plainly rather than substituting a stale or unrelated frame.

---

## Security review round, 2026-08-30

No CRITICAL and no HIGH. One MEDIUM and three LOW, all acted on. The two that
mattered are recorded here in full, because one of them is a functional
regression this fix introduced.

### The channel arm: a regression this patch created, now repaired

The socket-scoped arm serves TWO prefixes:

```python
if chat_id.startswith('local:') or chat_id.startswith('channel:'):
    socket_id = chat_id[len('local:') :]
    owner_id = get_user_id_from_session_pool(socket_id)
```

`chat_id[len('local:'):]` is a fixed six-character slice. A `channel:` id is
eight characters of prefix, so it yields `l:<channel id>`.

**Making the slice prefix aware does NOT fix it**, and that is worth stating
because it is the obvious reading of an off-by-two. The segment after `channel:`
is a CHANNEL id, not a socket id, so `get_user_id_from_session_pool` returns
`None` for it however the string is sliced. The owner never resolved, and the
bare `user.role != 'admin'` term was the only way any caller ever passed that
comparison.

So flag-gating that term, with `ENABLE_ADMIN_CHAT_ACCESS` false, would have made
a channel generation uncancellable by anyone at all, the channel's own members
included. That is a regression introduced by the fix, not a pre-existing gap
declined. It is repaired here rather than deferred, and "not reachable from
today's frontend" was explicitly not allowed to decide it: reachability is a
property of today's callers, not of the code.

The repair routes `channel:` ids through channel entitlement, resolved the way
`main.py` already resolves it when such a task is CREATED (membership for a
group or direct-message channel, an AccessGrants write grant otherwise), and
deliberately WITHOUT that gate's own `user.role != 'admin'` shortcut, so this
patch does not introduce a new unflagged bypass while removing five. Stopping a
channel task is therefore slightly stricter than creating one, which is the safe
direction. `ENABLE_ADMIN_CHAT_ACCESS` is not consulted on that arm at all: it
means cross-tenant CHAT access, and applying it to a channel-scoped id purely
because the two shared one branch would be a conflation rather than a decision.

Observed, in both legs:

```
pre-fix source: main.py WITHOUT the #1511 patch
  ok: PRE-FIX: a channel member could NOT stop their own channel's generation, because the socket slice never resolved one (listed=[], refused=404)

post-fix source: with the #1511 patch
  ok: a channel member lists and stops their channel's generation (listed=['task-for-channel:5f0b7e3a-1111-2222-3333-444455556666'], refused=None)
  ok: a non-member is refused on the channel arm (listed=[], refused=404)
```

Marker count moves from 5 to 8: the helper, plus a channel arm in each endpoint.

### The guards now assert identity rather than counts

The review's MEDIUM: the Dockerfile guard counted occurrences, so it could not
tell a benign survivor from a bypass. It was measured and confirmed NOT vacuous,
9 to 4 bare and 0 to 5 markers, so it does catch a dropped patch. It was blind to
two mutations, both of which were built and run.

The same shape appeared on the sibling branch for issue #1508, where the AST
postcondition matched call NAMES rather than call nodes. Two guards built the
same wrong way is a habit rather than an accident, so both were redesigned around
the same rule: **assert that the specific decision at the specific site is
correct, not that some number of things appear somewhere.**

Here that means four things, all node-based or text-identity based:

* every `user.role` comparison is classified by spelling, and the NEGATED ones
  must number exactly the four reviewed survivors;
* each of those four survivors is pinned by its own full line, so one cannot be
  displaced by something else wearing its count;
* the POSITIVE spelling is pinned too, at five rewritten sites plus the one
  pre-existing benign use, because a bypass written as `user.role == 'admin' or
  ...` is invisible to a count of the negated form by construction;
* each of the five must genuinely be conjoined with the flag, asked of the node.

The Dockerfile carries the same set, plus a line asserting the old two-prefix
branch header is gone.

All three review mutations now fail at build time:

```
# mutation B, swap a benign survivor for a real bypass, totals conserved
AssertionError: an unflagged admin bypass survived patching: "if chat is None or (chat.user_id != user.id and user.role != 'admin'):"

# mutation B2, same swap with a novel spelling no stale-string matches
AssertionError: the surviving bare admin check "if not ENABLE_PUBLIC_ACTIVE_USERS_COUNT and user.role != 'admin':" is no longer present exactly once in main.py (found 0)

# mutation A, a new bypass written in the positive form
AssertionError: expected exactly 6 `user.role == 'admin'` comparisons in main.py after patching, found 7. An extra one is a bypass written in the positive form
```

### Both test findings

**The session-pool stub answered for every socket.** `lambda socket_id: VICTIM_ID`
meant the None branch, the wrong-socket branch and the reconnected-socket branch
were all invisible, which is exactly the path the channel slice breaks. It is now
a real dict holding one socket, and three scenarios drive it:

```
  ok: the owner of a temporary chat still lists and stops it by their own socket id (listed=['task-for-local:sid-victim-socket'], refused=None)
  ok: a stranger naming someone else's socket is refused (listed=[], refused=404)
  ok: an unresolvable socket id denies rather than admits, even for the owner (listed=[], refused=404)
```

The third is the fail-closed assertion, and the OWNER is deliberately the caller
in it: an arm that fell open on an unresolvable socket would still pass with a
stranger.

**Site five was pinned by predicate TEXT, not by position.** A refactor that kept
the line verbatim and moved it somewhere it never executes passed. It is now
located in the AST: exactly one `is_chat_owner` gate on an `else` branch of
`chat_completion`, its predicate flag-gated and carrying no bare admin term, and
its body still raising:

```
the chat-completions ownership check
  ok: the completions-path ownership check no longer carries a bare admin term
  ok: the chat-completions handler is still called chat_completion
  ok: exactly one is_chat_owner gate on an else branch of the chat-completions handler (found 1)
  ok: and its predicate is flag-gated (unparsed: not await Chats.is_chat_owner(chat_id, user.id) and (not (user.role == 'admin' and ENABLE_ADMIN_CHAT_ACCESS)))
  ok: and carries no bare admin term (unparsed above)
  ok: and the guarded body still raises rather than having been emptied
```

The section heading no longer says "structural only", because the accurate
reading was never "the code is right, just not run" but "the predicate text is
right, the surrounding control flow is unasserted". The control flow is asserted
now.

### The no-op mutant, re-run against the widened suite

```
FAILED: 7 check(s)
  - the #1511 patch applied: 8 # hive (#1511) markers (found 0)
  - a non-owner administrator is handed NO task ids while the flag is off (observed ['task-for-e85bb8ac-32f1-4bcb-a5af-2c56060ce571'])
  - and cancels nothing, refused with 404 (refused=None, cancelled=['e85bb8ac-32f1-4bcb-a5af-2c56060ce571'])
  - a channel member lists and stops their channel's generation (listed=[], refused=404)
  - the completions-path ownership check no longer carries a bare admin term
  - and its predicate is flag-gated (unparsed: not await Chats.is_chat_owner(chat_id, user.id) and user.role != 'admin')
  - and carries no bare admin term (unparsed above)
```

### One finding deliberately not fixed here

`routers/channels.py` carries twenty-five occurrences of the same unflagged
predicate and zero `# hive (#1186)` markers, so it was never in the family
patch's scope. Filed as issue #1533 together with `yjs_document_leave`, framed as
a recurring class rather than as a fourth site to patch by reflex.

### Substrate

Every transcript above was produced in Docker. The suite runs in
`python:3.12-slim`; the patch and drift-guard measurements run inside
`ghcr.io/open-webui/open-webui@sha256:9fcea9c6e32ab60b0498f3986c6cdf651ddbe61db48d2213a3d28048ddd673d4`,
whose `/app/backend/open_webui/main.py` was confirmed byte-identical to the
vendored copy before any of it was trusted.

---

## CodeRabbit bot round, 2026-08-30

The CLI stream returned two findings, recorded above. The bot then fired a third
time once this pull request left draft, and found one thing the CLI, the security
reviewer and I had all missed.

### A guard line that could never match, written in the commit that fixed two others

```
    && test "$(grep -c '^    if chat_id.startswith(.local:.) or chat_id.startswith(.channel:.):\$' $B/main.py)" -eq 0 \
```

Single quoted, so the shell passes a literal `\$` through to `grep`, and in a
basic regular expression that matches a dollar CHARACTER rather than end of line.
Measured inside the pinned image:

```
single-quoted with backslash-dollar (the shipped line): 0
single-quoted, plain dollar:                            2
```

Zero before the patch and zero after it. The neighbouring survivor pins are
correct because they are double quoted, where the shell converts the escape
before `grep` sees it, which is exactly why the inconsistency was invisible on a
read.

This is the third time this round that a guard turned out to verify nothing, and
it was written in the commit that rebuilt these guards around identity because
two earlier ones could not fail. So every line of the consolidated guard is now
measured individually rather than as one `&&` chain, before and after the full
twenty-patch chain, inside the pinned image:

```
=== BEFORE the patch chain ===        === AFTER the patch chain ===
 1 want=4    got=0                     1 want=4    got=4
 ...                                   ...
17 want=8    got=0                    17 want=8    got=8
18 want=4    got=9                    18 want=4    got=4
19 want=6    got=1                    19 want=6    got=6
20 want=1    got=1                    20 want=1    got=1
21 want=1    got=1                    21 want=1    got=1
22 want=1    got=1                    22 want=1    got=1
23 want=1    got=1                    23 want=1    got=1
24 want=1    got=1                    24 want=1    got=1
25 want=0    got=2                    25 want=0    got=0
26 want=0    got=1                    26 want=0    got=0
```

Every line moves except 20 to 24, and those are the survivor pins, which are
meant to read one in BOTH states: their job is to catch a survivor being
displaced, not to detect the patch. Line 25 is the fixed anchor, two to zero.
Line 18 is the bare admin count, nine to four; line 19 the positive spelling, one
to six.

Running the guard as a single chain, which is how the earlier verification did
it, cannot distinguish a line that moves from a line that is always true. That is
what let this through, and it is why the per-line measurement is now the record.

### The channel helper's collaborators were unasserted

The helper is the only new code this patch introduces, and every stub in the
suite is `async` by construction, so a mock cannot tell a real `async def` from a
synchronous one or from a name `main.py` never imported. Either mistake ships a
container that raises on the first channel-scoped request while the suite stays
green.

Now asserted against the vendored source, which was confirmed byte-identical to
the pinned image before any of this was trusted:

```
  ok: main.py imports Channels, which the channel helper calls
  ok: main.py imports AccessGrants, which the channel helper calls
  ok: models/channels.py declares `get_channel_by_id` as async, so awaiting it in the helper is correct
  ok: models/channels.py declares `is_user_channel_member` as async, so awaiting it in the helper is correct
  ok: models/access_grants.py declares `has_access` as async, so awaiting it in the helper is correct
```

### The stale marker count

The adoption re-verification section said every claim below it was re-measured
from scratch while still showing five markers, because it was written before the
channel-arm repair moved the count to eight. It now carries a note naming which
of its claims are superseded and which still hold. Not retro-edited into
agreement: those transcripts are the record of what was observed at the time, and
rewriting them would turn this log into a summary.
