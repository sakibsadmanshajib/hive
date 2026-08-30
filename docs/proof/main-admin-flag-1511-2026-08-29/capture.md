# main.py chat task endpoints ignored ENABLE_ADMIN_CHAT_ACCESS: capture log, 2026-08-29

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
alone: they guard model access under `BYPASS_MODEL_ACCESS_CONTROL`, a public
active-users count under `ENABLE_PUBLIC_ACTIVE_USERS_COUNT`, and two feature
permissions. None is a chat-ownership decision. The drift guard pins that count
at 4 so a future one cannot appear unnoticed.

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
