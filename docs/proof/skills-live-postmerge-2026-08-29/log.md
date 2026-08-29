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

## 6. Two findings this capture produced

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
