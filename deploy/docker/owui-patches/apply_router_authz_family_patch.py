"""Build-time tenant isolation for the unflagged admin-bypass family (issue #1186).

MEDIUM family sweep. PR #960 gated the knowledge LISTING routes on
BYPASS_ADMIN_ACCESS_CONTROL and PR #1183 fixed the knowledge by-id reads, but
every other router still short-circuits on `user.role == 'admin'` WITHOUT
consulting the flag: write gates shaped `(not owner) and (no grant) and
role != admin`, read gates shaped `owner or admin or grant`, deny-shaped
gates like notes/chats/models, and response metadata such as the notes
write_access indicator. On this shared chat instance every tenant OWNER is an
instance admin, so role alone granted access to any tenant's resources.

skills.py joined this family later (PR #1388 shipped the skills surface,
issue #1186 was filed before that and never grew a skills.py entry). Its
listing routes (GET /, GET /list, GET /export) already flag-gate correctly,
matching the #960 predicate from day one, but the by-id read, toggle, update,
access-update and delete routes still short-circuit on bare
`user.role == 'admin'`, the exact shape this patch already fixes on every
sibling router. This is not the same hole as #1396 (the skills half of which
PR #1388 already closed, in the separate apply_skill_group_grants_patch.py,
untouched by this file): that one is about which access GRANTS a non-admin
write may attach, this one is about the admin role bypass on read/write of
the resource itself.

This patch rewrites every unflagged tenant-visibility bypass in the routers
listed by issue #1186 to the #960 predicate: access iff owner, or AccessGrants
grant, or admin when BYPASS_ADMIN_ACCESS_CONTROL is set. chats.py keeps its
own dedicated upstream flag ENABLE_ADMIN_CHAT_ACCESS instead, matching its
already-gated sibling routes (:1056, :1174, :1592). docker-compose.yml sets
both flags false.

False positives verified and left alone:
- knowledge.py reindex (:331): admin-global infrastructure by design.
- feature-permission gates (`role != admin AND not has_permission(...)`,
  e.g. notes.py :70/:118/..., tools.py :509, folders.py :53): these guard a
  FEATURE permission, not another tenant's resource; the admin bypass there
  is upstream-intended.
- files.py :828 `file_user.role != 'admin'`: requires the file OWNER to be an
  admin, not a bypass.
- chats.py community-sharing gates (:482/:531) and calendar automations gate
  (:57): feature permissions, not tenant visibility.

Every edit asserts its own effect and fails the build otherwise, so an
open-webui digest bump whose source shifted breaks loudly instead of silently
reverting to cross-tenant writes and reads. HIVE_OWUI_ROUTERS_DIR overrides
the target directory for scripts/test_owui_knowledge_authz.py.
"""

import ast
import os
import pathlib

ROUTERS = pathlib.Path(
    os.environ.get(
        "HIVE_OWUI_ROUTERS_DIR",
        "/app/backend/open_webui/routers",
    )
)

MARKER = "# hive (#1186)"
FLAGGED = "(user.role == 'admin' and BYPASS_ADMIN_ACCESS_CONTROL)"

# Each entry: (file, old, new, expected_pre_count).
# Every rewrite inserts exactly one "# hive (#1186)" marker comment, so the
# per-file marker totals asserted by the Dockerfile build stage and by
# scripts/test_owui_knowledge_authz.py are derivable from this table.
EDITS = [
    # ---------------- knowledge.py (BYPASS already imported) ----------------
    (
        "knowledge.py",
        "        )\n        and user.role != 'admin'\n    ):",
        "        )\n"
        "        and not " + FLAGGED + "  # hive (#1186)\n"
        "    ):",
        9,
    ),
    (
        "knowledge.py",
        "    if file.user_id != user.id and user.role != 'admin':\n",
        "    # hive (#1186): flag-gate the per-file read check on file/add\n"
        "    if file.user_id != user.id and not " + FLAGGED + ":\n",
        1,
    ),
    (
        "knowledge.py",
        "    if delete_file and (file.user_id == user.id or user.role == 'admin'):\n",
        "    # hive (#1186): deleting the underlying file object cross-tenant\n"
        "    # requires the same explicit flag\n"
        "    if delete_file and (\n"
        "        file.user_id == user.id or " + FLAGGED + "\n"
        "    ):\n",
        1,
    ),
    (
        "knowledge.py",
        "    if user.role != 'admin':\n        for file in files:",
        "    # hive (#1186): flag-gate the batch per-file read checks\n"
        "    if not " + FLAGGED + ":\n        for file in files:",
        1,
    ),
    # ---------------- files.py (BYPASS already imported) ----------------
    (
        "files.py",
        "    if file.user_id == user.id or user.role == 'admin'"
        " or await has_access_to_file(id, 'read', user, db=db):\n",
        "    if file.user_id == user.id or " + FLAGGED + ""
        " or await has_access_to_file(id, 'read', user, db=db):"
        "  # hive (#1186)\n",
        6,
    ),
    (
        "files.py",
        "    if file.user_id == user.id or user.role == 'admin'"
        " or await has_access_to_file(id, 'write', user, db=db):\n",
        "    if file.user_id == user.id or " + FLAGGED + ""
        " or await has_access_to_file(id, 'write', user, db=db):"
        "  # hive (#1186)\n",
        3,
    ),
    (
        "files.py",
        "                        knowledge.user_id == user.id\n"
        "                        or user.role == 'admin'\n"
        "                        or await AccessGrants.has_access(",
        "                        knowledge.user_id == user.id\n"
        "                        or "
        + FLAGGED + "  # hive (#1186)\n"
        "                        or await AccessGrants.has_access(",
        1,
    ),
    # ---------------- evaluations.py (adds BYPASS import) ----------------
    (
        "evaluations.py",
        "from open_webui.constants import ERROR_MESSAGES\n"
        "from open_webui.events import EVENTS, publish_event\n"
        "from open_webui.internal.db import get_async_session\n",
        "from open_webui.constants import ERROR_MESSAGES\n"
        "from open_webui.events import EVENTS, publish_event\n"
        "from open_webui.config import BYPASS_ADMIN_ACCESS_CONTROL"
        "  # hive (#1186)\n"
        "from open_webui.internal.db import get_async_session\n",
        1,
    ),
    (
        "evaluations.py",
        "    if user.role == 'admin':\n        feedback = await Feedbacks.",
        "    if user.role == 'admin' and BYPASS_ADMIN_ACCESS_CONTROL:"
        "  # hive (#1186)\n        feedback = await Feedbacks.",
        2,
    ),
    (
        "evaluations.py",
        "    if user.role == 'admin':\n"
        "        success = await Feedbacks.delete_feedback_by_id(id=id, db=db)",
        "    if user.role == 'admin' and BYPASS_ADMIN_ACCESS_CONTROL:"
        "  # hive (#1186)\n"
        "        success = await Feedbacks.delete_feedback_by_id(id=id, db=db)",
        1,
    ),
    # ---------------- folders.py (adds BYPASS import) ----------------
    (
        "folders.py",
        "from open_webui.config import UPLOAD_DIR\n",
        "from open_webui.config import BYPASS_ADMIN_ACCESS_CONTROL, UPLOAD_DIR"
        "  # hive (#1186)\n",
        1,
    ),
    (
        "folders.py",
        "    if folder and (user.role == 'admin'"
        " or await _has_folder_access(user.id, folder, 'read', db)):\n",
        "    if folder and (" + FLAGGED + ""
        " or await _has_folder_access(user.id, folder, 'read', db)):"
        "  # hive (#1186)\n",
        1,
    ),
    (
        "folders.py",
        "        if not folder or (user.role != 'admin'"
        " and not await _has_folder_access(user.id, folder, 'write', db)):\n",
        "        if not folder or (not " + FLAGGED + ""
        " and not await _has_folder_access(user.id, folder, 'write', db)):"
        "  # hive (#1186)\n",
        1,
    ),
    (
        "folders.py",
        "    # Only owner, admin, or write-granted user can update access\n"
        "    if user.role != 'admin' and user.id != folder.user_id:\n",
        "    # Only owner, admin, or write-granted user can update access\n"
        "    # hive (#1186): flag-gate the admin bypass\n"
        "    if not " + FLAGGED + " and user.id != folder.user_id:\n",
        1,
    ),
    (
        "folders.py",
        "    is_admin = user.role == 'admin'\n",
        "    is_admin = " + FLAGGED + "  # hive (#1186)\n",
        1,
    ),
    (
        "folders.py",
        "            if user.role != 'admin' and not await _has_folder_access("
        "user.id, folder, 'write', db):\n",
        "            if not " + FLAGGED + " and not await _has_folder_access("
        "user.id, folder, 'write', db):\n"
        "                # hive (#1186)\n",
        1,
    ),
    (
        "folders.py",
        "        elif folder and not folder.parent_id:\n"
        "            # Root shared folders can only be deleted by owner/admin\n"
        "            if user.role != 'admin':\n",
        "        elif folder and not folder.parent_id:\n"
        "            # Root shared folders can only be deleted by owner/admin\n"
        "            # hive (#1186): owner/admin here means the explicit flag,\n"
        "            # not bare instance-admin role\n"
        "            if not " + FLAGGED + ":\n",
        1,
    ),
    # ---------------- calendar.py (adds BYPASS import) ----------------
    (
        "calendar.py",
        "from open_webui.constants import ERROR_MESSAGES\n"
        "from open_webui.events import EVENTS, publish_event\n"
        "from open_webui.models.access_grants import AccessGrants\n",
        "from open_webui.constants import ERROR_MESSAGES\n"
        "from open_webui.events import EVENTS, publish_event\n"
        "from open_webui.config import BYPASS_ADMIN_ACCESS_CONTROL"
        "  # hive (#1186)\n"
        "from open_webui.models.access_grants import AccessGrants\n",
        1,
    ),
    (
        "calendar.py",
        "    if cal.user_id == user.id or user.role == 'admin':\n        return cal\n",
        "    # hive (#1186): single choke point for every calendar CRUD route;\n"
        "    # admin passes only when BYPASS_ADMIN_ACCESS_CONTROL is set\n"
        "    if cal.user_id == user.id or " + FLAGGED + ":\n        return cal\n",
        1,
    ),
    # ---------------- chats.py (uses its own upstream flag) ----------------
    (
        "chats.py",
        "    if user.role == 'admin':\n        chat = await Chats.get_chat_by_id(id, db=db)",
        "    if user.role == 'admin' and ENABLE_ADMIN_CHAT_ACCESS:"
        "  # hive (#1186)\n        chat = await Chats.get_chat_by_id(id, db=db)",
        3,
    ),
    (
        "chats.py",
        "    if chat.user_id != user.id and user.role != 'admin':\n",
        "    if chat.user_id != user.id"
        " and not (user.role == 'admin' and ENABLE_ADMIN_CHAT_ACCESS):"
        "  # hive (#1186)\n",
        4,
    ),
    # ---------------- prompts.py (BYPASS already imported) ----------------
    (
        "prompts.py",
        "        )\n        and user.role != 'admin'\n    ):",
        "        )\n"
        "        and not " + FLAGGED + "  # hive (#1186)\n"
        "    ):",
        6,
    ),
    (
        "prompts.py",
        "            user.role == 'admin'\n            or prompt.user_id == user.id\n",
        "            " + FLAGGED + "  # hive (#1186)\n"
        "            or prompt.user_id == user.id\n",
        1,
    ),
    (
        "prompts.py",
        "    if not (\n        user.role == 'admin'\n        or prompt.user_id == user.id\n",
        "    # hive (#1186): flag-gate the admin term\n"
        "    if not (\n        " + FLAGGED + "\n        or prompt.user_id == user.id\n",
        4,
    ),
    # ---------------- notes.py (BYPASS already imported) ----------------
    (
        "notes.py",
        "    if user.role != 'admin' and (\n        user.id != note.user_id",
        "    # hive (#1186): flag-gate the admin bypass\n"
        "    if not " + FLAGGED + " and (\n        user.id != note.user_id",
        5,
    ),
    (
        "notes.py",
        "        user.role == 'admin'\n        or (user.id == note.user_id)\n",
        "        " + FLAGGED + "  # hive (#1186)\n        or (user.id == note.user_id)\n",
        1,
    ),
    # ---------------- tools.py (BYPASS already imported) ----------------
    (
        "tools.py",
        "        if (\n            user.role == 'admin'\n            or tools.user_id == user.id\n",
        "        if (\n            " + FLAGGED + "  # hive (#1186)\n"
        "            or tools.user_id == user.id\n",
        1,
    ),
    (
        "tools.py",
        "        )\n        and user.role != 'admin'\n    ):",
        "        )\n"
        "        and not " + FLAGGED + "  # hive (#1186)\n"
        "    ):",
        9,
    ),
    # ---------------- models.py (BYPASS already imported) ----------------
    (
        "models.py",
        "    if not knowledge_items or user.role == 'admin':\n        return\n",
        "    # hive (#1186): flag-gate the defense-in-depth file-read skip\n"
        "    if not knowledge_items or " + FLAGGED + ":\n        return\n",
        1,
    ),
    (
        "models.py",
        "        if (\n            user.role == 'admin'\n            or model.user_id == user.id\n",
        "        if (\n            " + FLAGGED + "  # hive (#1186)\n"
        "            or model.user_id == user.id\n",
        1,
    ),
    (
        "models.py",
        "        )\n        and user.role != 'admin'\n    ):",
        "        )\n"
        "        and not " + FLAGGED + "  # hive (#1186)\n"
        "    ):",
        2,
    ),
    (
        "models.py",
        "    if (\n        user.role != 'admin'\n        and model.user_id != user.id\n",
        "    # hive (#1186): flag-gate the admin bypass (deny-shaped gate)\n"
        "    if (\n        not " + FLAGGED + "\n        and model.user_id != user.id\n",
        1,
    ),
    # ---------------- skills.py (BYPASS already imported) ----------------
    # Shared by GetSkillById and ToggleSkillById, both shaped
    # `user.role == 'admin' or skill.user_id == user.id or ...has_access(...)`.
    (
        "skills.py",
        "            user.role == 'admin'\n            or skill.user_id == user.id\n",
        "            " + FLAGGED + "  # hive (#1186)\n"
        "            or skill.user_id == user.id\n",
        2,
    ),
    # Shared by UpdateSkillById, UpdateSkillAccessById and DeleteSkillById,
    # all three shaped `skill.user_id != user.id and not ...has_access(write)
    # and user.role != 'admin'`.
    (
        "skills.py",
        "        )\n        and user.role != 'admin'\n    ):",
        "        )\n"
        "        and not " + FLAGGED + "  # hive (#1186)\n"
        "    ):",
        3,
    ),
]

# Per-file expected marker totals AFTER patching; the Dockerfile build stage
# asserts these exact counts with grep -c so a vendor bump that drops any
# single fix fails the image build.
EXPECTED_MARKERS = {
    "knowledge.py": 12,
    "files.py": 10,
    "evaluations.py": 4,
    "folders.py": 7,
    "calendar.py": 2,
    "chats.py": 7,
    "prompts.py": 11,
    "notes.py": 6,
    "tools.py": 10,
    "models.py": 5,
    "skills.py": 5,
}


def main():
    already = all(
        (ROUTERS / f).read_text().count(MARKER) == n
        for f, n in EXPECTED_MARKERS.items()
    )
    if already:
        print("apply_router_authz_family_patch: already applied")
        return

    for filename, old, new, expected in EDITS:
        target = ROUTERS / filename
        text = target.read_text()
        n = text.count(old)
        assert n == expected, (
            f"{filename}: anchor found {n} times, expected {expected}; "
            f"upstream open-webui source shifted, patch needs updating. "
            f"Anchor head: {old[:90]!r}"
        )
        patched = text.replace(old, new)
        ast.parse(patched)  # fails the build if a rewrite produced invalid Python
        target.write_text(patched)

    total = 0
    for filename, expected in EXPECTED_MARKERS.items():
        count = (ROUTERS / filename).read_text().count(MARKER)
        assert count == expected, (
            f"{filename}: {count} markers after patching, expected {expected}"
        )
        total += count
    print(
        f"apply_router_authz_family_patch: flag-gated {total} unflagged "
        f"admin bypasses across {len(EXPECTED_MARKERS)} routers (#1186)"
    )


if __name__ == "__main__":
    main()

