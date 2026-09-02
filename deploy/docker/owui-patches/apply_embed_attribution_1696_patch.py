"""Build-time splice: attribute Open WebUI's embedding spend to the user who
caused it, instead of to the shared shim account (issue #1696).

`agenerate_openai_batch_embeddings` is the single point every embedding on this
deployment leaves through. `RAG_EMBEDDING_ENGINE` is `openai` in compose, so
`generate_embeddings` takes the openai arm, and both callers reach that arm:
`get_embedding_function`'s closure (retrieval queries, and web search indexing
through `save_docs_to_vector_db`, which builds its own function with the same
factory) and `save_docs_to_vector_db`'s document ingest. The sync sibling
`generate_openai_batch_embeddings` has no caller anywhere in the pinned image,
so it is deliberately not patched; patching a function nothing calls is how a
splice starts looking like coverage it does not have.

The `user` argument is already threaded to this function by upstream, for the
`ENABLE_FORWARD_USER_INFO_HEADERS` feature. That is what makes this a header
injection rather than a plumbing change: the identity is already here, it was
simply never turned into a credential edge-api could bill against.

Fail closed. `hive_embed_attribution.attach` raises when no signed-in user
resolves, which aborts the embedding rather than sending it under the shim key.
On the web-search path that raise is caught by `process_web_search`'s
save-to-vector-db handler, which apply_web_search_embed_failure_1609_patch.py
already turned into a visible error, so the user sees a failed search instead of
a silent one. On the query path it surfaces as a 500 from the retrieval router.
Both are louder than the alternative, which is spending a customer's money on a
platform account and reporting success.

Applied here rather than in vendor/open-webui because the chat image builds only
the FRONTEND from the vendored tree (Dockerfile.open-webui), so a backend edit
under vendor/ is inert. The exact pinned literal is asserted below: a digest bump
that moves it fails the image build loudly instead of silently restoring the
mis-attribution.
"""

import ast
import os
import pathlib

TARGET = pathlib.Path(
    os.environ.get(
        'HIVE_OWUI_RETRIEVAL_UTILS_PY',
        '/app/backend/open_webui/retrieval/utils.py',
    )
)

MARKER = '# hive (#1696)'

OLD = """    headers = {
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

NEW = """    headers = {
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
    # Raises rather than degrading when no user resolves. Falling back to the
    # shared key is the defect, not the safety net (D-034).
    from open_webui.utils import hive_embed_attribution

    headers = await hive_embed_attribution.attach(headers, user)

    async with aiohttp.ClientSession(
        trust_env=True, timeout=aiohttp.ClientTimeout(total=AIOHTTP_CLIENT_TIMEOUT)
    ) as session:
        async with session.post(
            f'{url}/embeddings',
"""

text = TARGET.read_text()

assert text.count(OLD) == 1, (
    'the agenerate_openai_batch_embeddings request block is not present exactly '
    'once -- upstream open-webui source shifted, patch needs updating'
)
assert text.count(MARKER) == 0, (
    'the hive (#1696) marker is already present -- patch applied twice, or the '
    'pinned image already carries it'
)

# The splice only reaches the openai arm if generate_embeddings still routes
# there, and only helps if the user is still threaded through. Both are
# upstream's, not ours, so assert them rather than assume them.
assert 'embeddings = await agenerate_openai_batch_embeddings(' in text, (
    'generate_embeddings no longer dispatches to agenerate_openai_batch_embeddings '
    '-- patch needs updating'
)
assert 'user: UserModel = None,' in text, (
    'agenerate_openai_batch_embeddings no longer takes a user -- the identity '
    'this patch attaches is gone, patch needs updating'
)

patched = text.replace(OLD, NEW, 1)
assert patched.count(MARKER) == 1, 'the marker was not inserted exactly once'
ast.parse(patched)  # never write a module that cannot be imported
TARGET.write_text(patched)
