"""Build-time splice: insert tenant_role_from_db.py's fragment into
get_user_role() right before its final `return role`, so a live tenant
role lookup runs as a fallback when Open WebUI's own OAuth-claim role
resolution (structurally unable to see custom claims for Supabase's
third-party OIDC id_token, see the fragment's own header comment) leaves
`role` at its DEFAULT_USER_ROLE fallback.

Asserts its own effect and fails the build otherwise -- same pattern as
this Dockerfile's branding patches, so a future open-webui digest bump
whose upstream source shifted breaks the build loudly instead of
silently reverting to the unpatched (broken) behaviour. Checking only
that the anchor string exists is not enough: a rename of one of the
locals the fragment depends on (`user`, `user_data`, `role`) would leave
the anchor untouched, pass that check, and still silently regress to
everyone-pending at runtime. So this also carves out the enclosing
`get_user_role` function body and asserts its signature and the `role`
local are still there before splicing into it.
"""

import pathlib
import re

TARGET = pathlib.Path("/app/backend/open_webui/utils/oauth.py")
ANCHOR = "        return role\n"
SIGNATURE = "    async def get_user_role(self, user, user_data):\n"
FRAGMENT_PATH = pathlib.Path("/tmp/tenant_role_from_db.py")

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

fragment = FRAGMENT_PATH.read_text()
indented = "\n".join(
    ("        " + line if line.strip() else line) for line in fragment.splitlines()
)
TARGET.write_text(text.replace(ANCHOR, indented + "\n" + ANCHOR, 1))
