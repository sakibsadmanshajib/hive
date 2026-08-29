# User-created skills on the demo box: the post-merge capture

Branch: `feat/user-created-skills-coverage`
Date: 2026-08-29
Follows: PR #1388, merged 2026-08-29T10:00:09Z

PR #1388's own capture log named what it could not exercise, in its own words:
"single sign-on, the Caddy front end, and the gateway hop". It ran a standalone
container against a throwaway volume with inference pointed at Groq. This is
that missing capture, taken against `https://chat-hive.scubed.co` after the
merge deployed (`deploy-demo-box.yml` run 33261985611, `908e48a90`, success).

## Substrate

The deployed demo box, unmodified. Signed in through the real "Continue with
Hive" hop, so Supabase, the console origin, the OIDC exchange and Caddy are all
in the path. The account is an ordinary member, `role = user`, not a platform
admin and not the account the owner shows prospects. Its session was minted
through the admin one-time-token flow in
`apps/web-console/tests/e2e/support/live-auth.mjs`, which reads no password and
writes none.

No URL below carries a credential in its query string. The OIDC callback is the
one hop that does, and the capture script redacts `code`, `state` and every
token parameter before anything is written down, in the string the script owns
rather than after the fact.

## 1. The permission reached an already-booted database

This is the claim most able to be true in the repository and false on the box,
because `user.permissions` is persisted config and the environment loses to the
database after first boot. Read straight out of the running container's own
store, `/data/webui.db`, rather than inferred from compose:

```
user.permissions => {"workspace": {"models": false, "knowledge": false,
  "prompts": false, "tools": false, "skills": true, "models_import": false,
  "models_export": false, "prompts_import": false, "prompts_export": false,
  "tools_import": false, "tools_export": false, "skills_import": false,
  "skills_export": false}, "sharing": {..., "skills": false,
  "public_skills": false, ...}, ...}
```

`workspace.skills` is `true` on the deployed volume. Every sibling is untouched
at its upstream default, `sharing.skills` and `sharing.public_skills` are both
still `false`, and no flat dotted permission row exists. That is the #722 trap
cleared on the real substrate.

Worth recording because a later reader will otherwise re-derive it: the config
table on this deployment is key/value (`key`, `value`, `updated_at`), not the
single JSON `data` blob some of the older notes describe, and the live database
is `/data/webui.db`, not `/app/backend/data/webui.db`, which exists at zero
bytes and is a decoy.

## 2. The route survives the proxy, and the removed one still does not

```
GET https://chat-hive.scubed.co/skills            -> 200
GET https://chat-hive.scubed.co/workspace/skills  -> 404
```

Both halves matter. The new surface is reachable and the surface issue #772
removed has not come back with it.

## 3. Authoring, end to end, through the deployed front end

```
signed in, landed on https://chat-hive.scubed.co/
=== create a skill ===
shot sk2-01-editor-filled at https://chat-hive.scubed.co/skills/create
save control visible: true
HTTP 200 https://chat-hive.scubed.co/api/v1/skills/create
HTTP 200 https://chat-hive.scubed.co/api/v1/skills/
HTTP 200 https://chat-hive.scubed.co/api/v1/skills/list?page=1
after save url https://chat-hive.scubed.co/skills
shot sk2-02-after-save at https://chat-hive.scubed.co/skills
=== the index lists it ===
HTTP 200 https://chat-hive.scubed.co/api/v1/skills/list?page=1
skill listed on the index: true
shot sk2-03-index at https://chat-hive.scubed.co/skills
=== the composer offers it ===
HTTP 200 https://chat-hive.scubed.co/api/v1/skills/
HTTP 200 https://chat-hive.scubed.co/api/v1/skills/list?
skill offered in the $ mention list: true
shot sk2-04-mention at https://chat-hive.scubed.co/
shot sk2-05-composed at https://chat-hive.scubed.co/
sent the turn
HTTP 200 https://chat-hive.scubed.co/api/chat/completions
shot sk2-06-answer at https://chat-hive.scubed.co/c/f63117d9-aa3f-4f42-9c2a-f99c4c7fdd59
marker present in the rendered transcript: false
error-shaped text on screen: credits.
```

An ordinary member reached the Skills navigation row, opened the editor, saved a
skill, saw it listed, and had it offered by the `$` mention picker in the
composer, all against the deployed stack.

## 4. What this capture could NOT prove, stated plainly

The model's answer. The turn was composed with the skill attached and sent, the
request reached `/api/chat/completions` and returned 200, and the surface then
rendered "You're out of credits." (frame `sk2-06-answer`). The account holds no
credit, so no completion was produced and the marker could not appear. Nothing
about the skills path failed here.

The injected half is not unproven in general: PR #1388's capture proved it
against the identical tree, one chat and one model, the same question twice,
where only the turn carrying the skill ended in a token that exists nowhere but
the skill body. What is unproven is that specific hop on this specific box with
a funded account, and it stays unproven rather than being asserted. Funding an
account was out of scope for this run and is separately owned.

## 5. Cleanup

The skill this capture created was deleted through the product's own route, so
the box carries no litter from it:

```
/skills enumerated 47 interactive control(s); committed floor is 1
skills visible to this account before cleanup: 1
delete live-proof-1788024347 -> HTTP 200
/skills enumerated 42 control(s) with zero skills owned
```

The two enumeration counts are also what backs the floor this branch commits:
the surface renders 42 controls with no skills owned at all, so a presence bar
of 1 is far below anything a healthy run sees, and only an emptied surface
trips it.

## 6. What this capture found, before review added to it

**The surface was outside the coverage denominator.** `surfaces.ts` swept
`home`, `sidebar`, `model-picker`, the three composer surfaces, `user-menu`,
`search`, `workspace`, the chat menus and the settings and workspace tabs.
Nothing swept `/skills`. A frontend regression, a new proxy rule, or
`workspace.skills` reverting to its upstream default would each have emptied
the surface with every gate still green. This branch sweeps and floors it.

**The duplicate-name error names a field the author never filled in.**
`skill.id` is unique instance wide and the editor slugifies it from the name, so
two accounts naming a skill the same thing collide, and upstream answers with
"Uh-oh! This id is already registered. Please choose another id string." The
holder is almost always a skill the author cannot read, so they search their own
library, find nothing, and read it as a broken product. Issue #1397 and PR #1437
scope uniqueness to the tenant, which is the real fix; this branch fixes the
sentence, which still has to make sense for the many accounts on this box that
have no tenant group at all and therefore share one namespace regardless.

## 7. The `surfaces=^skills$` sweep, and the harness defect it found

Review asked for one dispatch of `chat-coverage.yml` with `surfaces=^skills$`,
on the grounds that adding a surface to the denominator without ever running
the prove pass over it is an unchecked claim. Correct, and the run answered
more than the question.

**CI could not run it.** Run `33267620156` failed its own input check before
Playwright started: `the live sweep needs: HIVE_QA_AGENT_EMAIL
SUPABASE_ANON_KEY SUPABASE_SERVICE_ROLE_KEY`. All three are empty in this
repository's Actions environment. That stream is SKIPPED, not passed, and is
filed as issue #1459.

**Run directly against the deployed box instead**, driving the real engine
rather than a description of it: the same `STATIC_SURFACES` entry, the same
`enumerate` with the same `SELECTOR`, the same delta subtraction, the same
`isDestructive` and `isStateful` partition, the same `proveByClick` and the
real `valuePass` from `persistence.ts`.

**It aborted before reaching /skills**, on the home baseline:

```
baseline NOT pinned: the sidebar is not closed and neither toggle is on
screen, so this surface cannot be pinned into a known state
```

Measured rather than guessed. On the deployed shell:

```
/open sidebar/i  -> count=0 firstVisible=false     (sidebar open)
/close sidebar/i -> count=1 firstVisible=true
AFTER COLLAPSE /open sidebar/i  -> count=2
AFTER COLLAPSE /close sidebar/i -> count=0
```

Two controls named "Open Sidebar" exist once the sidebar closes. Playwright's
strict mode makes `isVisible()` on a two-element locator throw, and
`ensureSidebar`'s `.catch(() => false)` turned that throw into "not visible",
so the function reported a sidebar it had just successfully collapsed as
impossible to pin. Every surface that pins the sidebar shut inherited it, which
is `sidebar` plus every `clickTop` surface, so the live gate could not complete
against the shipped shell at all. `.first()` on both locators fixes it.

**With that fixed, the sweep:**

```
baseline sidebar pinned: true
baseline (home, sidebar collapsed): 27 controls
/skills enumerated 13 raw, 3 after the delta subtraction
  New Skill [a] / Search Skills [input] / Select view [button]
clickable: 2, stateful: 1

{ "skills": { "total": 3, "proven": 3, "deferred": 0, "unproven": [] } }

PROVEN  Search Skills [input]  proof=value     -> hive-coverage-50095
PROVEN  New Skill [a]          proof=navigate  /skills -> /skills/create
PROVEN  Select view [button]   proof=dom       what is on screen changed

3 controls, 3 proven, 0 not
```

The 13-to-3 subtraction is the `delta` correction measured: ten of the thirteen
were the application shell, now attributed where they belong rather than
counted twice under this surface.

One correction worth recording, because a wrong red misleads as much as a wrong
green. An earlier attempt reported `Search Skills` unproven. That was an error
in the driver, not the surface: it routed a stateful control through
`proveByClick` when the spec routes it through `valuePass`. The corrected run
is the one above.

This is the empty-index case, with the account owning no skills, and it is the
weakest case rather than the strongest: a populated index adds one row and one
active switch per skill, which is exactly the account-data variability the
`dataDriven` classification exists to keep out of a pinned count.

## 8. The collision message, measured on the box rather than quoted

The wording this branch replaces was read out of `constants.py` and argued
about. It is worth having reached it the way an author does, so the claim that
it is confusing is a measurement rather than a reading.

Two saves of the same name, as the same ordinary member, through the deployed
editor:

```
=== first "Research" ===
HTTP 200 https://chat-hive.scubed.co/api/v1/skills/create
landed on https://chat-hive.scubed.co/skills
=== second "Research", expecting the collision ===
HTTP 400 https://chat-hive.scubed.co/api/v1/skills/create
still on https://chat-hive.scubed.co/skills/create
toast text on screen: Uh-oh! This id is already registered. Please choose another id string.
=== cleanup ===
delete research -> HTTP 200
```

So the path is reachable, the author is left on a filled-in form that did not
save, and the sentence they get names an "id string" while the field they
filled in was the name. The id the box assigned was the bare `research`,
unprefixed, which is the pre-#1437 scoping: `c205afb45` had merged but had not
yet reached this deployment when this was captured.

The replacement string is not shown here, because the box runs the merged
image and this branch's frontend is not on it. What exists today is the unit
cover in `lib/hive/skill-save-error.test.ts` plus the source pin on the create
route; the deployed capture of the new wording belongs after this merges and
`deploy-demo-box.yml` runs, which is where the orchestrator contract puts it.

A first attempt at this capture screenshotted six seconds after the click and
found no toast, because it had already faded, and reported "(none found)".
Recorded because that reads exactly like a missing error message, which is a
different defect from the one being measured, and a run that produced it
without explanation would have been misleading in a new direction.
