"""Build-time rewrite of how this deployment decides an Open WebUI role.

Two edits, one file (`open_webui/utils/oauth.py`), both about the same thing:
nothing may hand out instance admin except the control plane's explicit
platform-admin attribute (issue #748).

1. Splice `tenant_role_from_db.py`'s fragment into `get_user_role()` right
   before its final `return role`, so a live lookup against Hive's own Postgres
   decides the role. Open WebUI's own OAuth-claim resolution is structurally
   unable to see custom claims for Supabase's third-party OIDC id_token (see
   the fragment's header), so without this every login stays at
   DEFAULT_USER_ROLE.

2. Delete stock Open WebUI's two single-user admin promotions. Upstream
   promotes the only user of the instance to admin, once at login time and once
   after inserting a new user. That is sound for a personal deployment and
   wrong for this one: this Open WebUI instance is shared by every Hive tenant,
   so on a fresh volume, a restore, or any database reset, whichever account
   authenticated first became the administrator of everyone's chat, regardless
   of tenant, and stayed admin until a second account existed. The login-time
   one also sits ABOVE the splice point in (1), so the Hive lookup never ran
   for that login at all.

Every edit asserts its own effect and fails the build otherwise, same pattern
as this Dockerfile's branding patches, so a future open-webui digest bump whose
upstream source shifted breaks the build loudly instead of silently reverting to
the unpatched behaviour. Checking only that an anchor string exists is not
enough: a rename of one of the locals the fragment depends on (`user`,
`user_data`, `role`) would leave the anchor untouched, pass that check, and
still silently regress to everyone-pending at runtime. So this also carves out
the enclosing `get_user_role` function body and asserts its signature and the
`role` local are still there before splicing into it.

Paths default to the image's own layout and are overridable by environment
variable so scripts/test_owui_tenant_role.py can run this against a copy of the
pinned image's oauth.py rather than a lookalike.
"""

import os
import pathlib
import re

TARGET = pathlib.Path(
    os.environ.get("HIVE_OWUI_OAUTH_PY", "/app/backend/open_webui/utils/oauth.py")
)
ANCHOR = "        return role\n"
SIGNATURE = "    async def get_user_role(self, user, user_data):\n"
FRAGMENT_PATH = pathlib.Path(
    os.environ.get("HIVE_TENANT_ROLE_FRAGMENT", "/tmp/tenant_role_from_db.py")
)

# Upstream's login-time promotion, verbatim. Inside get_user_role, above the
# splice anchor.
LOGIN_PROMOTION = (
    "        if user and user_count == 1:\n"
    "            # If the user is the only user, assign the role \"admin\" - actually repairs role for single user on login\n"
    "            log.debug('Assigning the only user the admin role')\n"
    "            return 'admin'\n"
)
LOGIN_PROMOTION_REPLACEMENT = (
    "        # hive (issue #748): upstream promoted the only user of the whole\n"
    "        # instance to admin here, on every login. This Open WebUI instance is\n"
    "        # shared by every Hive tenant, so that made whichever account happened\n"
    "        # to authenticate first after a fresh volume, a restore or a database\n"
    "        # reset an administrator of everyone's chat. It also returned above the\n"
    "        # Hive tenant lookup spliced in below, so that login never reached the\n"
    "        # real role resolution at all. Instance admin is resolved from the\n"
    "        # control plane's explicit platform-admin attribute instead, so this\n"
    "        # bootstrap is deleted rather than narrowed. Upstream's comment in the\n"
    "        # next branch still says admin promotion is deferred to post-insert:\n"
    "        # not on this deployment, that promotion is deleted too.\n"
)

# Upstream's post-insert promotion, verbatim. In handle_callback, right after
# the new user is inserted. Its own comment is included so removing the code
# does not leave the comment describing something that no longer happens.
POST_INSERT_PROMOTION = (
    "                    # Atomically check if this is the only user *after* the\n"
    "                    # insert to avoid TOCTOU race on first-user registration.\n"
    "                    # Matches signup_handler pattern.\n"
    "                    if await Users.get_num_users(db=db) == 1:\n"
    "                        await Users.update_user_role_by_id(user.id, 'admin', db=db)\n"
    "                        user = await Users.get_user_by_id(user.id, db=db)\n"
)
POST_INSERT_PROMOTION_REPLACEMENT = (
    "                    # hive (issue #748): upstream promoted the first user to\n"
    "                    # admin here, immediately after inserting it. Same reason as\n"
    "                    # the login-time promotion above: on a shared instance the\n"
    "                    # first account to arrive is not an operator, it is just the\n"
    "                    # first customer. The role this account was provisioned with\n"
    "                    # is the one get_user_role resolved, and it stands.\n"
)

text = TARGET.read_text()
assert text.count(SIGNATURE) == 1, (
    "get_user_role's signature not found exactly once -- upstream "
    "open-webui source shifted (renamed the method or its params), "
    "patch needs updating"
)
assert text.count(ANCHOR) == 1, (
    "get_user_role's 'return role' anchor not found exactly once -- "
    "upstream open-webui source shifted, patch needs updating"
)

# The function body is everything from its signature to the next
# same-or-lesser-indented `def`/`async def` (or end of file).
sig_start = text.index(SIGNATURE)
body_start = sig_start + len(SIGNATURE)
next_def = re.search(r"\n    (?:async )?def ", text[body_start:])
body_end = body_start + next_def.start() if next_def else len(text)
body = text[body_start:body_end]

assert ANCHOR in body, (
    "'return role' anchor is not inside get_user_role's own body -- "
    "upstream open-webui source shifted, patch needs updating"
)
assert re.search(r"\brole\s*=", body), (
    "get_user_role no longer assigns a `role` local -- upstream "
    "open-webui source shifted, patch needs updating"
)
assert "user_data" in body, (
    "get_user_role no longer references `user_data` -- upstream "
    "open-webui source shifted, patch needs updating"
)

assert LOGIN_PROMOTION in body, (
    "upstream's login-time single-user admin promotion is not in "
    "get_user_role's body in the form this patch removes. Either upstream "
    "changed it (re-read the new form and update LOGIN_PROMOTION) or it is "
    "already gone. Do not skip this check: leaving that branch in place hands "
    "instance admin to whichever account signs in first on a shared instance "
    "(issue #748)"
)
assert text.count(LOGIN_PROMOTION) == 1, (
    "upstream's login-time single-user admin promotion appears "
    f"{text.count(LOGIN_PROMOTION)} times, expected exactly one"
)
assert text.count(POST_INSERT_PROMOTION) == 1, (
    "upstream's post-insert single-user admin promotion appears "
    f"{text.count(POST_INSERT_PROMOTION)} times, expected exactly one. That "
    "block promotes the first account inserted to admin, which on a shared "
    "instance is a cross-tenant grant (issue #748)"
)

fragment = FRAGMENT_PATH.read_text()
indented = "\n".join(
    ("        " + line if line.strip() else line) for line in fragment.splitlines()
)
patched = text.replace(ANCHOR, indented + "\n" + ANCHOR, 1)
patched = patched.replace(LOGIN_PROMOTION, LOGIN_PROMOTION_REPLACEMENT, 1)
patched = patched.replace(POST_INSERT_PROMOTION, POST_INSERT_PROMOTION_REPLACEMENT, 1)

assert LOGIN_PROMOTION not in patched, "login-time admin promotion survived the rewrite"
assert POST_INSERT_PROMOTION not in patched, "post-insert admin promotion survived the rewrite"
assert "hive tenant-role lookup failed" in patched, "tenant-role fragment was not spliced"

TARGET.write_text(patched)
