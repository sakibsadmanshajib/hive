"""Build-time tenant isolation for the RAG collection filter (issue #1186).

HIGH slice. retrieval/utils.py filter_accessible_collections kept an unflagged
admin bypass (`if user.role == 'admin': return safe_names`), so POST
/retrieval/query/collection returned another tenant's RAG document chunks for
any known knowledge-base id — re-opening the #1056 leak through the documented
RAG path. The compose comment shipped with PR #1183 claiming Knowledge was
fully closed therefore overclaimed; that comment is softened in the same PR
that adds this patch.

Enforcement matches the #960 listing predicate: access iff owner, or
AccessGrants read grant, or admin when BYPASS_ADMIN_ACCESS_CONTROL is set.
docker-compose.yml sets the flag false, so a plain instance admin is validated
like any other user at this choke point (it also feeds /query/doc and web
search collection allowlisting).

Every edit asserts its own effect and fails the build otherwise, so an
open-webui digest bump whose source shifted breaks loudly instead of silently
reverting to cross-tenant reads. HIVE_OWUI_RETRIEVAL_UTILS_PY overrides the
target path for scripts/test_owui_knowledge_authz.py.
"""

import ast
import os
import pathlib

TARGET = pathlib.Path(
    os.environ.get(
        "HIVE_OWUI_RETRIEVAL_UTILS_PY",
        "/app/backend/open_webui/retrieval/utils.py",
    )
)

MARKER = "# hive (#1186)"

IMPORT_OLD = "from open_webui.config import (\n    RAG_EMBEDDING_CONTENT_PREFIX,"
IMPORT_NEW = (
    "from open_webui.config import (\n"
    "    BYPASS_ADMIN_ACCESS_CONTROL,  # hive (#1186)\n"
    "    RAG_EMBEDDING_CONTENT_PREFIX,"
)

BYPASS_OLD = "    if user.role == 'admin':\n        return safe_names"
BYPASS_NEW = (
    "    # hive (#1186): instance admin no longer bypasses collection access\n"
    "    # checks here unless BYPASS_ADMIN_ACCESS_CONTROL is set; matches the\n"
    "    # PR #960 listing predicate used by the knowledge routes.\n"
    "    if user.role == 'admin' and BYPASS_ADMIN_ACCESS_CONTROL:\n"
    "        return safe_names"
)

text = TARGET.read_text()
if text.count(MARKER) == 2:
    print("apply_retrieval_authz_patch: already applied")
    raise SystemExit(0)

assert text.count(IMPORT_OLD) == 1, (
    "open_webui.config import block not found exactly once in "
    "retrieval/utils.py; upstream open-webui source shifted, patch needs updating"
)
assert text.count(BYPASS_OLD) == 1, (
    "unflagged admin return in filter_accessible_collections not found "
    "exactly once; upstream open-webui source shifted, patch needs updating"
)

patched = text.replace(IMPORT_OLD, IMPORT_NEW, 1)
patched = patched.replace(BYPASS_OLD, BYPASS_NEW, 1)

assert patched.count(MARKER) == 2, "expected two #1186 markers after patching"
assert patched.count("user.role == 'admin' and BYPASS_ADMIN_ACCESS_CONTROL") == 1, (
    "flag-gated admin check missing after patching"
)
assert BYPASS_OLD not in patched
ast.parse(patched)  # fails the build if the rewrite produced invalid Python

TARGET.write_text(patched)
print(
    "apply_retrieval_authz_patch: flag-gated filter_accessible_collections "
    "admin bypass (POST /retrieval/query/collection cross-tenant chunk read)"
)
