"""Create one throwaway admin account inside an isolated proof stack.

Runs inside the chat container, the same way scripts/owui-mint-admin-token.py
does. The account exists only in this stack's own sqlite volume, is not the
owner's account and is not the shared demo or e2e fixture account, and its
password is a fresh random value that is hashed immediately and never printed,
so nothing shared is rotated (docs/live-test-auth.md).

Why an admin is needed at all: every endpoint this proof exercises is
`get_admin_user`, so probing them without an admin session would return 401
and prove nothing about the proxy. The 404s in the log are therefore Caddy
refusing a request that Open WebUI would have accepted.
"""
import asyncio
import sys
import uuid

sys.path.insert(0, "/app/backend")

from open_webui.models.auths import Auths  # noqa: E402
from open_webui.models.users import Users  # noqa: E402
from open_webui.utils.auth import get_password_hash  # noqa: E402

EMAIL = "proof-admin@hive-verify-736.invalid"


async def main() -> None:
    existing = await Users.get_user_by_email(EMAIL)
    if existing is not None:
        print(existing.id)
        return
    user = await Auths.insert_new_auth(
        email=EMAIL,
        password=await get_password_hash(uuid.uuid4().hex),
        name="Proof Admin 736",
        role="admin",
    )
    if user is None:
        raise SystemExit("could not create the proof admin account")
    print(user.id)


asyncio.run(main())
