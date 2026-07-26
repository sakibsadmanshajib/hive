"""Build-time splice: insert tenant_role_from_db.py's fragment into
get_user_role() right before its final `return role`, so a live tenant
role lookup runs as a fallback when Open WebUI's own OAuth-claim role
resolution (structurally unable to see custom claims for Supabase's
third-party OIDC id_token, see the fragment's own header comment) leaves
`role` at its DEFAULT_USER_ROLE fallback.

Asserts its own effect (the anchor line exists exactly once, and the
fragment marker is present afterward) and fails the build otherwise --
same pattern as this Dockerfile's branding patches, so a future
open-webui digest bump whose upstream source shifted breaks the build
loudly instead of silently reverting to the unpatched (broken) behaviour.
"""

import pathlib

TARGET = pathlib.Path("/app/backend/open_webui/utils/oauth.py")
ANCHOR = "        return role\n"
FRAGMENT_PATH = pathlib.Path("/tmp/tenant_role_from_db.py")

text = TARGET.read_text()
assert text.count(ANCHOR) == 1, (
    "get_user_role's 'return role' anchor not found exactly once -- "
    "upstream open-webui source shifted, patch needs updating"
)

fragment = FRAGMENT_PATH.read_text()
indented = "\n".join(
    ("        " + line if line.strip() else line) for line in fragment.splitlines()
)
TARGET.write_text(text.replace(ANCHOR, indented + "\n" + ANCHOR, 1))
