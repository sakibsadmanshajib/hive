"""Build-time splice: attribute Open WebUI's embedding spend to the user who
caused it, instead of to the shared shim account (issue #1696).

Three files, because the identity has to be present at the chokepoint before the
chokepoint can use it.

`retrieval/utils.py` carries the chokepoint. `agenerate_openai_batch_embeddings`
is the single point every embedding on this deployment leaves through:
`RAG_EMBEDDING_ENGINE` is `openai` in compose, so `generate_embeddings` takes the
openai arm, and both producers reach that arm, `get_embedding_function`'s closure
(retrieval queries, and web search indexing through `save_docs_to_vector_db`,
which builds its own function with the same factory) and document ingest. The
sync sibling `generate_openai_batch_embeddings` has no caller anywhere in the
pinned image, so it is deliberately not patched; patching a function nothing
calls is how a splice starts looking like coverage it does not have.

The `user` argument is already threaded to that function by upstream, for the
`ENABLE_FORWARD_USER_INFO_HEADERS` feature. That is what makes the third file a
header injection rather than a plumbing change: the identity is already there for
the two producers above, and it was simply never turned into a credential
edge-api could bill against.

`routers/knowledge.py` and `tools/builtin.py` are the two producers where it is
NOT already there. `embed_knowledge_base_metadata` calls the embedding function
with no user at all, and so does the `query_knowledge_bases` builtin. Both would
otherwise be refused by the chokepoint, and the first fails inside its own
swallowed `except`, so knowledge base metadata embedding would stop working with
no visible error and the admin reindex endpoint would report zero of N. Every
call site already has the identity in scope, so both are threaded rather than
left broken: fixing mis-attribution by silently breaking ingest is not a trade
this repository makes.

Fail closed. `hive_embed_attribution.attach` raises when the destination is not
the Hive gateway or when no signed-in user resolves, which aborts the embedding
rather than sending it under the shim key. On the web-search path that raise is
caught by `process_web_search`'s save-to-vector-db handler, which
apply_web_search_embed_failure_1609_patch.py already turned into a visible error,
so the user sees a failed search instead of a silent one. On the query path it
surfaces as a 500 from the retrieval router. Both are louder than the
alternative, which is spending a customer's money on a platform account, or
sending a customer's session bearer to a host an instance admin chose.

Applied here rather than in vendor/open-webui because the chat image builds only
the FRONTEND from the vendored tree (Dockerfile.open-webui), so a backend edit
under vendor/ is inert. Every literal below is asserted: a digest bump that moves
one fails the image build loudly instead of silently restoring the
mis-attribution.
"""

import ast
import os
import pathlib

MARKER = '# hive (#1696)'


def target(env_var: str, default: str) -> pathlib.Path:
    return pathlib.Path(os.environ.get(env_var, default))


UTILS = target('HIVE_OWUI_RETRIEVAL_UTILS_PY', '/app/backend/open_webui/retrieval/utils.py')
KNOWLEDGE = target('HIVE_OWUI_KNOWLEDGE_PY', '/app/backend/open_webui/routers/knowledge.py')
BUILTIN = target('HIVE_OWUI_BUILTIN_PY', '/app/backend/open_webui/tools/builtin.py')

# --------------------------------------------------------------------------
# 1. retrieval/utils.py: attach the carrier at the chokepoint.
# --------------------------------------------------------------------------

UTILS_OLD = """    headers = {
        'Content-Type': 'application/json',
        'Authorization': f'Bearer {key}',
    }
    if ENABLE_FORWARD_USER_INFO_HEADERS and user:
        headers = include_user_info_headers(headers, user)

    async with aiohttp.ClientSession(
        trust_env=True, timeout=aiohttp.ClientTimeout(total=AIOHTTP_CLIENT_TIMEOUT)
    ) as session:
        async with session.post(
            f'{url}/embeddings',
"""

UTILS_NEW = """    headers = {
        'Content-Type': 'application/json',
        'Authorization': f'Bearer {key}',
    }
    if ENABLE_FORWARD_USER_INFO_HEADERS and user:
        headers = include_user_info_headers(headers, user)

    # hive (#1696): attribute this call to the signed-in user.
    #
    # Authorization above is RAG_OPENAI_API_KEY, which on this deployment is
    # OWUI_SHIM_KEY: one shared account for every tenant. Every embedding here
    # is real metered spend on Hive's own gateway, and a web search produces
    # one of these per chunk batch, so leaving it at the shim key billed the
    # platform for work the customer did and hid it from the customer's own
    # usage. edge-api reads the per-user token off this carrier header and
    # honours it only under the shim key (internal/auth/owui_unwrap.go).
    #
    # `url` is passed because attach REFUSES to forward a user credential to
    # anything but the gateway named in the environment. This `url` comes from
    # app.state.config.RAG_OPENAI_API_BASE_URL, which is persistent config an
    # instance admin can rewrite at runtime, and on this shared instance every
    # tenant OWNER is an instance admin. Without that check, moving from a
    # shared platform key to a per-user session bearer would turn one bad knob
    # into a cross-tenant session harvest.
    #
    # Raises rather than degrading when no user resolves. Falling back to the
    # shared key is the defect, not the safety net (D-034).
    from open_webui.utils import hive_embed_attribution

    headers = await hive_embed_attribution.attach(headers, user, url)

    async with aiohttp.ClientSession(
        trust_env=True, timeout=aiohttp.ClientTimeout(total=AIOHTTP_CLIENT_TIMEOUT)
    ) as session:
        async with session.post(
            f'{url}/embeddings',
"""

# --------------------------------------------------------------------------
# 2. routers/knowledge.py: give embed_knowledge_base_metadata a user, and hand
#    it one at all six call sites.
# --------------------------------------------------------------------------

KNOWLEDGE_SIGNATURE_OLD = """async def embed_knowledge_base_metadata(
    request: Request,
    knowledge_base_id: str,
    name: str,
    description: str,
) -> bool:
    \"\"\"Generate and store embedding for knowledge base.\"\"\"
    try:
        content = f'{name}\\n\\n{description}' if description else name
        embedding = await request.app.state.EMBEDDING_FUNCTION(content)
"""

KNOWLEDGE_SIGNATURE_NEW = """async def embed_knowledge_base_metadata(
    request: Request,
    knowledge_base_id: str,
    name: str,
    description: str,
    # hive (#1696): the user this embedding is performed for. Keyword-only with
    # a default so an upstream caller added by a digest bump still type checks;
    # what it does NOT do is let one bill the platform, because the gateway
    # refuses a call with no signed-in user rather than serving it under the
    # shared key. A caller that arrives here without a user is a caller whose
    # embedding fails loudly, which is the intended direction.
    user=None,
) -> bool:
    \"\"\"Generate and store embedding for knowledge base.\"\"\"
    try:
        content = f'{name}\\n\\n{description}' if description else name
        embedding = await request.app.state.EMBEDDING_FUNCTION(content, user=user)
"""

# The two multi-line call sites are byte identical (knowledge create, and update
# by id), so this replacement is expected to fire exactly twice.
KNOWLEDGE_MULTILINE_OLD = """        await embed_knowledge_base_metadata(
            request,
            knowledge.id,
            knowledge.name,
            knowledge.description,
        )
"""

KNOWLEDGE_MULTILINE_NEW = """        await embed_knowledge_base_metadata(
            request,
            knowledge.id,
            knowledge.name,
            knowledge.description,
            user=user,  # hive (#1696)
        )
"""

# The two external-source call sites are byte identical too.
KNOWLEDGE_EXTERNAL_OLD = """    await embed_knowledge_base_metadata(request, knowledge.id, knowledge.name, knowledge.description)
"""

KNOWLEDGE_EXTERNAL_NEW = """    await embed_knowledge_base_metadata(request, knowledge.id, knowledge.name, knowledge.description, user=user)  # hive (#1696)
"""

KNOWLEDGE_REINDEX_OLD = """        if await embed_knowledge_base_metadata(request, kb.id, kb.name, kb.description):
"""

KNOWLEDGE_REINDEX_NEW = """        if await embed_knowledge_base_metadata(request, kb.id, kb.name, kb.description, user=user):  # hive (#1696)
"""

KNOWLEDGE_UPDATE_OLD = """    await embed_knowledge_base_metadata(request, id, updated.name, updated.description)
"""

KNOWLEDGE_UPDATE_NEW = """    await embed_knowledge_base_metadata(request, id, updated.name, updated.description, user=user)  # hive (#1696)
"""

# --------------------------------------------------------------------------
# 3. tools/builtin.py: the knowledge-base search builtin.
# --------------------------------------------------------------------------

BUILTIN_OLD = """        query_embedding = await __request__.app.state.EMBEDDING_FUNCTION(query)
"""

BUILTIN_NEW = """        # hive (#1696): __user__ is a plain dict here; hive_embed_attribution
        # resolves it into a real user before reading a credential off it.
        query_embedding = await __request__.app.state.EMBEDDING_FUNCTION(query, user=__user__)
"""


def splice(path: pathlib.Path, replacements: list[tuple[str, str, int]], markers: int) -> None:
    """Apply every replacement to path, asserting the count of each first.

    The counts are stated per replacement rather than left at "at least one",
    because two of the knowledge.py call sites are byte identical pairs and a
    silent drop from two to one is exactly the shape that leaves half a feature
    broken.
    """
    text = path.read_text()
    assert text.count(MARKER) == 0, (
        f'the hive (#1696) marker is already present in {path} -- patch applied '
        'twice, or the pinned image already carries it'
    )
    for old, new, expected in replacements:
        found = text.count(old)
        assert found == expected, (
            f'expected {expected} occurrence(s) of an anchor in {path}, found {found} '
            '-- upstream open-webui source shifted, patch needs updating:\n'
            + old[:200]
        )
        text = text.replace(old, new)
    assert text.count(MARKER) == markers, (
        f'expected {markers} hive (#1696) marker(s) in {path} after the splice, '
        f'got {text.count(MARKER)}'
    )
    ast.parse(text)  # never write a module that cannot be imported
    path.write_text(text)


utils_text = UTILS.read_text()

# The splice only reaches the openai arm if generate_embeddings still routes
# there, only helps if the user is still threaded through, and the destination
# check has nothing to verify against if the url stops being a parameter. All
# three are upstream's, not ours, so assert them rather than assume them.
assert 'embeddings = await agenerate_openai_batch_embeddings(' in utils_text, (
    'generate_embeddings no longer dispatches to agenerate_openai_batch_embeddings '
    '-- patch needs updating'
)
assert 'user: UserModel = None,' in utils_text, (
    'agenerate_openai_batch_embeddings no longer takes a user -- the identity '
    'this patch attaches is gone, patch needs updating'
)
assert "url: str = 'https://api.openai.com/v1'," in utils_text, (
    'agenerate_openai_batch_embeddings no longer takes a url, so the destination '
    'check in hive_embed_attribution has nothing to verify against'
)

splice(UTILS, [(UTILS_OLD, UTILS_NEW, 1)], markers=1)

splice(
    KNOWLEDGE,
    [
        (KNOWLEDGE_SIGNATURE_OLD, KNOWLEDGE_SIGNATURE_NEW, 1),
        (KNOWLEDGE_MULTILINE_OLD, KNOWLEDGE_MULTILINE_NEW, 2),
        (KNOWLEDGE_EXTERNAL_OLD, KNOWLEDGE_EXTERNAL_NEW, 2),
        (KNOWLEDGE_REINDEX_OLD, KNOWLEDGE_REINDEX_NEW, 1),
        (KNOWLEDGE_UPDATE_OLD, KNOWLEDGE_UPDATE_NEW, 1),
    ],
    # One in the signature comment plus one per rewritten call site: 1 + 2 + 2 + 1 + 1.
    markers=7,
)

splice(BUILTIN, [(BUILTIN_OLD, BUILTIN_NEW, 1)], markers=1)
