# Visual proof: web search attaches sources again, and a dropped source is now loud (issue #1609, PR #1617)

Captured 2026-09-01 against a running stack built from this PR's branch. Two
runs, because this PR has two halves and the second one only shows itself when
the embedding call fails.

## What was running

Four containers on a private Docker network, built and started from the
worktree at commit `2ff09b459`:

| Container | Image | Role |
| --- | --- | --- |
| `pr1617-owui` | `hive-owui:proof-1617`, built from `deploy/docker/Dockerfile.open-webui` on this branch | the surface under test |
| `pr1617-litellm` | `ghcr.io/berriai/litellm:v1.98.0@sha256:20b5044b...` with the repo's own `deploy/litellm/config.yaml` | embeddings and chat routing |
| `pr1617-searxng` | `searxng/searxng:2026.8.29-d226b78bc@sha256:b36af798...` with the repo's own `deploy/searxng/settings.yml` | the search engine |
| `pr1617-pgvector` | `pgvector/pgvector:pg16` | the vector store, same `VECTOR_DB=pgvector` as the deployment |

`edge-api` and `control-plane` are not in this stack, and that is the one gap
worth stating plainly. This machine's `.env` has empty `S3_ENDPOINT` and
`NEXT_PUBLIC_SUPABASE_URL`, so both services `log.Fatalf` at boot on
`storage unavailable` and no full local stack can start here
(`.claude/skills/worktree-compose-stack.md`). Open WebUI's embeddings therefore
reach LiteLLM directly rather than through the gateway. Everything this PR
changes is upstream of that seam: the request shape Open WebUI builds, the
number of requests it opens at once, and what it does when one of them fails.

Composer configuration matched the deployment where it matters:
`HIVE_DEFAULT_FUNCTION_CALLING=legacy` (the compose default), which is what
routes a web search through `chat_web_search_handler` and
`process_web_search`, the path this issue is about. The first attempt at this
capture ran without it, took the native tool-calling `search_web` path
instead, made zero embedding calls and would have been a false green.

## Run 1: a web search that attaches sources

Screenshot: `pr1617-proof.png` (posted to the PR through
`scripts/post-pr-visual-proof.sh`).

Prompt, typed into the composer with the Web Search integration toggled on:

    What is the newest stable PostgreSQL release and when was it released?
    Answer from the web and cite the pages.

What the surface shows: `Retrieved 2 sources`, an answer naming PostgreSQL
18.6 and its release date, an inline `postgresql.org` citation chip, and an
expanded `2 Sources` list with both URLs.

Container log, Open WebUI (`docker logs pr1617-owui`):

    Fetching pages: 100%|##########| 10/10 [02:12<00:00, 13.23s/it]
    save_docs_to_vector_db:1734 - generating embeddings for web-search-75a687e7...
    save_docs_to_vector_db:1778 - embeddings generated 78 for 78 items
    save_docs_to_vector_db:1796 - added 78 items to collection web-search-75a687e7...
    query_doc:306 - query_doc:result [['f4fa12f1-...', '4e257296-...', '4b22df0b-...', ...]]
      [[{'title': "PostgreSQL: The world's most advanced open source database",
         'source': 'https://www.postgresql.org/', ...}]]

Compare the last line with the one the issue captured on the demo box, which
is the whole defect:

    query_doc:result [[]] [[]]

Container log, LiteLLM (`docker logs pr1617-litellm`), for the whole run:

    INFO: 172.24.0.5:41856 - "POST /v1/embeddings HTTP/1.1" 200 OK
    INFO: 172.24.0.5:46668 - "POST /v1/embeddings HTTP/1.1" 200 OK

Two requests for the whole search: one carrying all 78 chunks in a single
batch, one for the query embedding. Before this change that same search was 78
document requests plus the query, all of the first 78 in flight at once. That
is the measured form of the fix, on the running stack rather than against the
counting stub in `scripts/test_owui_embedding_burst.py`.

The reconcile half is visible in the boot line, which is what makes the two
values reach an already-booted volume at all:

    open_webui.config:seed_registered_defaults:89 - hive: reconciled Open WebUI
    config from env: ... rag.embedding_batch_size=100,
    rag.embedding_concurrent_requests=4, ...

## Run 2: a dropped source is now visible instead of silent

Screenshot: `pr1617-failure.png`.

Same stack, same prompt, with one change: `RAG_OPENAI_API_BASE_URL` pointed at
a closed port, so `save_docs_to_vector_db` fails the same way the 429 made it
fail on the demo box. Before this PR that produced `log.debug`, `status: True`
and a collection name nothing had been written to, so the user saw a normal
answer with no sources and a model denying it had web access.

What the surface shows now: `An error occurred while searching the web`, the
status the middleware emits on its own except branch, above the answer.

Container log, Open WebUI:

    save_docs_to_vector_db:1799 - Cannot connect to host pr1617-litellm:4999
      ssl:default [Connect call failed ('172.24.0.4', 4999)]
    process_web_search:2677 - hive (#1609): web search indexing failed, no
      sources attached

Three things that log proves at once. The failure is at ERROR rather than
DEBUG. The new marker line is running in the container, so the splice reached
the image rather than only the repository. And the internal host name appears
in the operator's log and nowhere in the user-visible status, which is the
leak `apply_audio_error_leak_patch.py` closes for #1562 and which a test in
`scripts/test_owui_embedding_burst.py` asserts against the detail string.

One thing in that screenshot is not this PR. The free route echoed a raw
`<dots_function_call>` block into its answer, which is a model behaviour on
`route-free-default` and is unrelated to the change under test; the line being
demonstrated is the grey status above it.

## The build itself

The image was built from this branch, and both build-time guards passed
against the pinned digest rather than against the vendored copy:

    #36 [stage-1 23/76] RUN python3 /tmp/apply_web_search_embed_failure_1609_patch.py
        && test "$(grep -c '# hive (#1609)' .../routers/retrieval.py)" -eq 1
        && ! grep -q 'error saving docs' .../routers/retrieval.py
        && python3 -c "...ast.parse..."
    #36 DONE 1.4s

    #37 [stage-1 24/76] RUN grep -q 'if concurrent_requests:' .../retrieval/utils.py
        && grep -q 'asyncio.Semaphore(concurrent_requests)' .../retrieval/utils.py
        && grep -q 'range(0, len(query), embedding_batch_size)' .../retrieval/utils.py
    #37 DONE 0.9s

Stage 37 is the guard added in review, closing the one drift path this PR
otherwise had: the shape half patches nothing, so nothing bound the image's
copy of `get_embedding_function` to the vendored copy the test measures. Both
files are byte identical between the pinned digest and `vendor/open-webui`
today:

    0f6b67ff6231ab176785f01666ced4281df7239dca18f32082a5ac51677a15ab  retrieval/utils.py
    20507937e884b7c2ba4da3815a57d57313d2f21fdfed7af5f62fec092357427d  routers/retrieval.py

Same two hashes from `sha256sum` inside the image and from the worktree.

## Live route measurement (review thread on batch size 100)

Both embedding routes were called directly, outside Open WebUI, with the exact
request shape this change now builds: a 100 element input array of 1000
character chunks.

    nvidia/llama-nemotron-embed-vl-1b-v2:free  n=1    body=1,069 B    HTTP 200  vectors=1    dim=2048  prompt_tokens=168
    nvidia/llama-nemotron-embed-vl-1b-v2:free  n=100  body=100,465 B  HTTP 200  vectors=100  dim=2048  prompt_tokens=16,800
    qwen/qwen3-embedding-8b                    n=1    body=1,051 B    HTTP 200  vectors=1    dim=4096  prompt_tokens=167
    qwen/qwen3-embedding-8b                    n=100  body=100,447 B  HTTP 200  vectors=100  dim=4096  prompt_tokens=16,700

Neither route caps the array below 100, so the failure mode raised in review,
every web search returning 502 rather than intermittently returning no
sources, does not arise on either.

## Credentials

Nothing in this capture carries one. The captured URLs are
`http://127.0.0.1:3399/` and the two public postgresql.org pages the search
returned; no query string in either screenshot or in this log holds a token,
code or key. The throwaway Open WebUI account used to drive the browser exists
only inside a container that has already been removed, and its session token
was never printed to any file here.
