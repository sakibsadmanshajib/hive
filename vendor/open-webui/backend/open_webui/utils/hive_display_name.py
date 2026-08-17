"""Derive a display name when the identity provider does not send one.

Open WebUI provisions an OAuth user with `user_data.get(OAUTH_USERNAME_CLAIM)`
and, when that claim is missing, stores the email address in the `name` column.
Five of six accounts on the Hive demo box are in exactly that state, so the
product greets people by their email address and every avatar initial is the
first letter of it.

The claim is missing for a real reason rather than a misconfiguration. Supabase's
OAuth authorization server issues a minimal third party OIDC token: standard
claims only, and no user metadata (confirmed live on both the shared runner and
the demo box, and recorded in `deploy/docker/docker-compose.yml` where the same
finding defeated `OAUTH_ROLES_CLAIM`). Nothing writes a name at sign up either,
since `apps/web-console` never collects one. So there is no claim to prefer here
that we are not already preferring, and inventing one would be worse than this.

What is left is the local part of the address, which in practice carries the
person's name. `sakib.shajib@example.com` becomes "Sakib Shajib", which is both
better than the raw address and honest about where it came from. The user can
correct it in Settings then Account, and that edit survives later sign ins
because Open WebUI only refreshes the name from a claim that is actually present.
"""

import re
import unicodedata

# Separators that stand in for a space inside an email local part.
_SEPARATORS = re.compile(r'[._\-]+')

# Longest display name this will produce. Long enough for any real name, short
# enough that a hostile address cannot push a wall of text through every surface
# that renders the value.
_MAX_LENGTH = 64


def display_name_from_email(email: str) -> str:
    """Best effort human name for an address. Falls back to the address itself.

    Never raises, and never returns an empty string for a non-empty address:
    a bad display name is a cosmetic problem, and failing a sign in over one
    would not be.
    """
    email = (email or '').strip()
    if not email:
        return ''

    local = email.split('@', 1)[0]
    # A quoted local part is legal but rare. The quotes are syntax, not a name.
    local = local.strip('"')
    # Plus addressing is a routing tag, not part of the person's name.
    local = local.split('+', 1)[0]

    words = [word for word in _SEPARATORS.split(_sanitize(local)) if word]
    if not words:
        return email

    # Capitalize unconditionally. `handle_callback` lower cases the address
    # before it reaches here, so there is no deliberate casing left to preserve
    # and a branch for it would be dead code that implies a promise this does not
    # keep.
    return ' '.join(word.capitalize() for word in words)[:_MAX_LENGTH].strip()


def _sanitize(value: str) -> str:
    """Drop characters that do not belong in a name rendered next to other
    people's names: control characters, and the bidirectional overrides that let
    one string display as another."""
    return ''.join(
        character
        for character in value
        if unicodedata.category(character) not in ('Cc', 'Cf', 'Co', 'Cs', 'Zl', 'Zp')
    )
