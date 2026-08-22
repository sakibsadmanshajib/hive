"""Build-time rewrite: stop Open WebUI storing a new user's email address as
their display name.

`handle_callback` provisions an OAuth user with the configured username claim
and, when that claim is missing, writes the email address into `name`. On this
deployment the claim is always missing, because Supabase's OAuth authorization
server issues a minimal third party OIDC token (standard claims only, no user
metadata, the same finding that defeated OAUTH_ROLES_CLAIM), and nothing
collects a name at sign up. Five of six accounts on the demo box were therefore
greeted by their own email address, in the chat header and in every avatar
initial.

This has to be a build-time rewrite rather than an edit under
`vendor/open-webui/backend`: this image builds only the front end from that
tree and takes its backend from the pinned upstream image, so a change to the
vendored backend is inert. Every other Hive backend change here works the same
way.

Asserts its own effect and fails the build otherwise, the same pattern as the
tenant-role and RAG-config patches beside it, so a future digest bump whose
upstream source shifted breaks the build loudly instead of silently reverting to
storing email addresses again.
"""

import ast
import pathlib

TARGET = pathlib.Path('/app/backend/open_webui/utils/oauth.py')

# The upstream fallback, verbatim, including its indentation inside
# handle_callback's signup branch.
ANCHOR = """                    name = user_data.get(username_claim)
                    if not name:
                        log.warning('Username claim is missing, using email as name')
                        name = email
"""

REPLACEMENT = """                    name = user_data.get(username_claim)
                    if not name:
                        # hive: derive a display name from the address instead of
                        # storing the address itself. The configured claim is
                        # still preferred; it is simply never present here.
                        name = display_name_from_email(email)
                        log.warning(
                            'hive: username claim %s is missing, derived the display name from the email address',
                            username_claim,
                        )
"""

IMPORT_ANCHOR = 'from open_webui.utils.auth import create_token, get_password_hash\n'
IMPORT_LINE = 'from open_webui.utils.hive_display_name import display_name_from_email\n'

text = TARGET.read_text()

assert text.count(ANCHOR) == 1, (
    "the email-as-name fallback was not found exactly once in oauth.py -- "
    'upstream open-webui source shifted, patch needs updating'
)
assert text.count(IMPORT_ANCHOR) == 1, (
    "oauth.py's open_webui.utils.auth import was not found exactly once -- "
    'upstream open-webui source shifted, patch needs updating'
)

# The refresh path must keep skipping an absent claim, or a name the user set in
# Settings then Account would be overwritten on their next sign in and this fix
# would last exactly one session.
assert 'if new_name and new_name != user.name:' in text, (
    'the sign in name refresh no longer guards on a present claim -- upstream '
    'open-webui source shifted, and a user-set display name would now be '
    'overwritten on every login'
)

patched = text.replace(ANCHOR, REPLACEMENT, 1).replace(
    IMPORT_ANCHOR, IMPORT_ANCHOR + IMPORT_LINE, 1
)

ast.parse(patched)
TARGET.write_text(patched)

print('hive: display name derived from the email local part when the claim is absent')
