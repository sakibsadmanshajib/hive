"""Build-time authz residuals for the #1190-family review (issue #1191).

Two sites the #1186 family sweep had left as verified false positives (see
apply_router_authz_family_patch.py) that review reclassified as residuals:

1. chats.py clone_shared_chat_by_id (:1603): the shared-chat grant check
   `if shared and user.role != 'admin' ...` let a bare instance admin skip
   AccessGrants enforcement entirely, so an admin could read (clone) any
   tenant's ALREADY-SHARED chat without ENABLE_ADMIN_CHAT_ACCESS or a grant.
   Rewritten to this router's own predicate: admin bypasses only when
   ENABLE_ADMIN_CHAT_ACCESS is set; otherwise owner-or-grant like everyone
   else. docker-compose.yml sets that flag false.
2. knowledge.py /reindex (:331): the bare role check let an instance admin
   trigger a global wipe+rebuild of every tenant's vector collections.
   Flag-gated on BYPASS_ADMIN_ACCESS_CONTROL (already imported in this
   router; the same flag the #960/#1056/#1186 knowledge family reads), so
   with compose defaults nobody can trigger a global reindex over the API;
   flipping the flag restores operator use.

Fail-loud posture, identical to the sibling authz patches: exact-literal
anchors with expected occurrence counts, ast.parse after every rewrite,
marker totals asserted at the end and again by the Dockerfile drift guard,
idempotent early-exit once applied. HIVE_OWUI_ROUTERS_DIR overrides the
target directory for scripts/test_owui_knowledge_authz.py.
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

MARKER = "# hive (#1191)"

# Each entry: (file, old, new, expected_pre_count). Each rewrite inserts
# exactly one MARKER comment, so per-file totals are chats.py 1 and
# knowledge.py 1. Anchors were verified unique against the vendored source.
EDITS = [
    (
        "chats.py",
        "    if shared and user.role != 'admin' and shared.user_id != user.id:\n",
        "    # hive (#1191): bare admin no longer skips the shared-chat grant\n"
        "    # check; same predicate as this router's other admin fallbacks\n"
        "    if (\n"
        "        shared\n"
        "        and not (user.role == 'admin' and ENABLE_ADMIN_CHAT_ACCESS)\n"
        "        and shared.user_id != user.id\n"
        "    ):\n",
        1,
    ),
    (
        "knowledge.py",
        "    if user.role != 'admin':\n"
        "        raise HTTPException(\n"
        "            status_code=status.HTTP_401_UNAUTHORIZED,\n"
        "            detail=ERROR_MESSAGES.UNAUTHORIZED,\n"
        "        )\n",
        "    # hive (#1191): reindex wipes and rebuilds EVERY tenant's vector\n"
        "    # collections, so bare instance-admin role is not enough; the\n"
        "    # explicit BYPASS_ADMIN_ACCESS_CONTROL flag is required, matching\n"
        "    # the knowledge family's cross-tenant escape hatch.\n"
        "    if not (user.role == 'admin' and BYPASS_ADMIN_ACCESS_CONTROL):\n"
        "        raise HTTPException(\n"
        "            status_code=status.HTTP_401_UNAUTHORIZED,\n"
        "            detail=ERROR_MESSAGES.UNAUTHORIZED,\n"
        "        )\n",
        1,
    ),
]

EXPECTED_MARKERS = {
    "chats.py": 1,
    "knowledge.py": 1,
}


def main():
    already = all(
        (ROUTERS / f).read_text().count(MARKER) == n
        for f, n in EXPECTED_MARKERS.items()
    )
    if already:
        print("apply_authz_residuals_1191_patch: already applied")
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
        f"apply_authz_residuals_1191_patch: flag-gated reindex and "
        f"shared-chat grant skip across 2 routers (#1191)"
    )


if __name__ == "__main__":
    main()
