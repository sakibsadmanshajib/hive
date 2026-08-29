# User-created skills: live capture log

Branch: `feat/user-created-skills`
Date: 2026-08-29
PR: #1388

## Substrate, stated plainly

`hive-open-webui:skills-proof`, built from this branch's tree with
`docker build -f deploy/docker/Dockerfile.open-webui .`, run standalone on the
development box against its own throwaway SQLite volume. Not the demo box, and
not a compose stack.

Inference goes to Groq's OpenAI-compatible endpoint rather than to the Hive
gateway. Both Hive keys in this checkout's `.env` are dead against
`api-hive.scubed.co`: `OWUI_SHIM_KEY` returns `401 Incorrect API key provided`
and `HIVE_API_KEY` returns `401 API key is revoked`, and minting a new one
needs Supabase credentials this box does not have (`SUPABASE_URL`,
`SUPABASE_ANON_KEY` and `SUPABASE_SERVICE_ROLE_KEY` are all empty here, so
`tests/e2e/support/live-auth.mjs` cannot run either). That gap does not touch
what is under test: the skill is injected into the request inside Open WebUI,
before any provider is chosen, so a real model turn through any
OpenAI-compatible endpoint exercises the same seam. What this capture does NOT
exercise, and what a post-merge capture on the demo box still should:
single sign-on, the Caddy front end, and the gateway hop.

Accounts are local to the throwaway container. Open WebUI makes its first
account an admin unconditionally, so a second account was created through the
admin add-user route with `role: user`; every capture below is that ordinary
member, not an admin. Their passwords are local throwaway values for a
container that was deleted after the run and are deliberately not recorded
here. No URL in this capture carries a credential in its query string.

Model: `openai/gpt-oss-20b`.

## 1. The permission reaches an already-booted database

`workspace.skills` is a leaf inside the single persisted `user.permissions`
config row, seeded on a container's first boot. So the environment variable
alone cannot reach a deployment that has already booted, which is the #722
trap. Two boots against the same volume, reading the row straight out of Open
WebUI's own database.

Boot one, `USER_PERMISSIONS_WORKSPACE_SKILLS_ACCESS` unset:

```
row present: True
workspace.skills = False
workspace.knowledge = False
sharing.skills = False
sharing.public_skills = False
chat.system_prompt = True
--- dotted rows that must not exist ---
dotted permission rows: []
```

Boot two, same volume, the variable set to `true`. Startup line:

```
2026-08-29 09:29:39.014 | INFO | open_webui.config:seed_registered_defaults:52 -
hive: reconciled Open WebUI config from env: automations.enable=False,
calendar.enable=False, memories.enable=False, notes.enable=False,
oauth.auto_redirect=False,
rag.embedding_model=sentence-transformers/all-MiniLM-L6-v2,
ui.enable_login_form=True, user.permissions={'workspace.skills': True},
web.search.enable=False
```

Row after:

```
row present: True
workspace.skills = True
workspace.knowledge = False
sharing.skills = False
sharing.public_skills = False
chat.system_prompt = True
--- dotted rows that must not exist ---
dotted permission rows: []
```

The leaf moved, every sibling is untouched, and no `user.permissions.*` row was
created. That last line is the point of the read-modify-write seam: a dotted
config row would persist and be read by nothing.

The ordinary member's own resolved permissions, from `GET /api/v1/auths/`:

```
role = user
workspace.skills = True
chat.system_prompt = True
chat.controls = True
```

## 2. A member creates a skill through the UI

`http://localhost:3131/skills`, signed in as the ordinary member.

- `sk-index.png`: the Skills row in the shell navigation, and the empty index.
- `sk-01-editor.png`: the editor at `/skills/create`, with the id slugified
  from the name (`hive-proof-format`) and the body filled in.
- `sk-02-saved.png`: back at the index, `Skills 1`, "Hive Proof Format",
  "By Proof Member", active.

The skill body is:

```
Answer the user's question in exactly one sentence. Then, on a new line, write
this exact marker and nothing else: HIVE-SKILL-PROOF-OK-2026-08-29. Always
include the marker, on every answer, no exceptions.
```

The marker exists nowhere else in the deployment, so its presence in an answer
can only come from this skill body reaching the model.

## 3. The skill changes the model's answer in a chat turn

`sk-03-turns.png`, one frame, one chat, one model, the same question twice.

Control turn, no skill:

> A git rebase takes the commits on one branch and "replays" them onto a new
> base commit, effectively rewinding the branch to that point and reapplying
> each change. This rewrites the commit history, giving each commit a new ID,
> and is often used to keep a linear history or incorporate changes from
> another branch without creating a merge commit.

Three sentences. No marker.

Skill turn, `$hive-proof-format` selected in the composer:

> A git rebase rewrites a branch by replaying its commits onto a new base
> commit, giving each commit a new ID and producing a linear history without
> merge commits.
> HIVE-SKILL-PROOF-OK-2026-08-29

One sentence. Marker present. That is the behaviour change, and it is the half
that was actually in doubt: storage round-tripping was never the risk.

The mention path was used deliberately rather than the composer's Skills
button. A mentioned skill always has its full body injected as a system
message (`utils/middleware.py`), while a merely selected one is offered as a
manifest entry plus a `view_skill` builtin tool, which makes the outcome depend
on the model choosing to call a tool. The injection seam is the same either
way; the mention removes the model's discretion from the proof.

## 4. The per-user System Prompt, folded in

Asked separately: does the System Prompt control render for an ordinary member,
and does a value set there reach inference. Both measured rather than read off
the defaults.

Settings > General renders the System Prompt textarea for this member, who is
`role = user`. `USER_PERMISSIONS_CHAT_CONTROLS` and
`USER_PERMISSIONS_CHAT_SYSTEM_PROMPT` both default to true upstream, and the
persisted tree above confirms `chat.system_prompt = True` on this deployment.

Set to:

```
Begin every single reply with the exact line: HIVE-SYSTEM-PROMPT-OK-2026-08-29
```

`sk-07-system-prompt.png`, a fresh chat afterwards:

> HIVE-SYSTEM-PROMPT-OK-2026-08-29
> Ensures code works as intended before deployment.

So it reaches inference. This is a different shape from #1360 and #1265, where
an artefact is mounted or catalogued and then never read: `Chat.svelte`
prepends the value to the outgoing message list unconditionally. No defect
filed, no code changed for it.

`sk-04`, `sk-05` and `sk-06` are the discarded attempts that preceded it, kept
because they show a real trap for the next person: navigating away resets the
composer's model to the first in the list, which here was a Llama Prompt Guard
model, and the turn then fails with "`tool calling` is not supported with this
model" rather than with anything about the system prompt.

## Incidental finding, not fixed here

Settings > General still shows the vendor's "Help us translate Hive Chat!" link
to `github.com/open-webui/...`. `owui-patches/hive_ui_surfaces.py` has a
`settings-vendor-translate-link` rewrite for exactly this, but that rewrite
runs against the pinned bundle the frontend stage then discards, so it never
reached the shipped frontend. Same class as the admin-panel link #949 fixed by
source deletion. Out of scope for this branch; noted so it is not lost.
