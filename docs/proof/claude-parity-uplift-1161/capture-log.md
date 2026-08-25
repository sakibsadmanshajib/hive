# Visual proof capture log, PR #1161

- Date: 2026-08-25
- Branch: feat/claude-visual-parity at 4e0779d17
- Image: hive-open-webui:v0.10.2-branded (sha256:efe32c9df69a332c4ee40a24771d5493cd6c891f8d763f5084c468daf52f85f6), built locally from this exact tree and verified before capture.
- Stack: standalone branded Open WebUI container (`parity1161-open-webui`, host port 127.0.0.1:13161) on a throwaway Docker network `parity1161` with a local stub LLM serving an OpenAI compatible `/v1/models` (two aliases carrying `name` and `description` copy, the same passthrough shape edge-api's catalog response uses) plus `/v1/chat/completions` streaming prose. Ephemeral in-container data directory; no shared account, shared database, or running demo stack was touched.
- Session: fresh first-run signup created a throwaway admin user on the ephemeral store, then one chat turn sent through the composer with the default model selected.

## Surfaces captured

1. `parity1161-01-transcript-light.png` (sha256 78c6102ae5d48260747d762ca80490511f70c0eee72222f654c34660b14ad87a): light theme full window after one assistant reply. Assistant prose renders in Source Serif 4 at the shared reading measure; sidebar, composer chrome, buttons and quick chips stay Hanken Grotesk; the model chip sits in the composer control row.
2. `parity1161-02-model-picker.png` (sha256 6242cbded521976af9cd65d9b1b17de22d18724ecfef86e984a1171c360bfc57): model picker open from the composer chip. Each row shows the alias name plus its one sentence purpose line rendered under it ("Everyday reasoning for drafting, analysis and careful long-form writing." / "Quick short answers for lookups, rewrites and small edits."), which is the catalog display_name/summary copy arriving through the models payload.
3. `parity1161-03-serif-closeup.png` (sha256 5dc0c6ac8bcb76313f069607e1539a7627e91ca0d13064bb45ebb27fda980397): element close-up of the assistant message body. Serif body text with a true italic emphasis phrase and a code span that stays mono.
4. `parity1161-04-dark-fullwindow.png` (sha256 17d62f1648e7e6af286b4900860be19d43f10028088429d61f14183fa0d87c37): dark theme full window of the same conversation via `localStorage.theme = dark` reload, exercising the boot-script branch that now also writes `data-theme="dark"` alongside the class. Dark register coherent: warm charcoal surfaces, serif transcript, grotesk chrome.

## Credential hygiene

No URL in any captured surface carries a credential: all navigation stayed on `http://127.0.0.1:13161` with plain path URLs (`/auth?redirect=%2F` carries only a redirect target, no token parameter). Signup used a throwaway account on an ephemeral in-container store, so nothing to rotate. No redaction required; verified against the lint:proof-tokens pattern set (no token/code query parameters, no bare JWT).

## Reproduce

```bash
docker network create parity1161
docker run -d --name parity1161-stub-llm --network parity1161 --network-alias stub-llm \
  -v /tmp/parity1161/stub_llm.py:/srv/stub.py:ro python:3.11-alpine python /srv/stub.py
docker run -d --name parity1161-open-webui --network parity1161 -p 127.0.0.1:13161:8080 \
  -e WEBUI_SECRET_KEY=<local-only> -e ENABLE_OPENAI_API=true \
  -e OPENAI_API_BASE_URL=http://stub-llm:9000/v1 -e OPENAI_API_KEY=<local-only> \
  -e ENABLE_OLLAMA_API=false hive-open-webui:v0.10.2-branded bash start.sh
```

Then sign up on first run, send one message, capture with Playwright at 1912x964 CSS pixels viewport.
