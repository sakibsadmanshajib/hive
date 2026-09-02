---
name: doc-layout
description: Read, summarize, or extract fields from a scanned contract, PDF, or photographed form through the doc-layout vision route.
---

# Doc-layout skill

Contract and PDF page understanding via a serverless vision route. No new
infrastructure: this skill is a prompt template plus the `route-doc-vlm`
LiteLLM route (`deploy/litellm/config.yaml`), request-shape contract in
`apps/agent-engine/internal/docvlm`. Issue #300.

## When to use

The task asks to read, summarize, extract fields from, or check the layout
of a document the agent cannot reliably parse as plain text (a scanned
contract, a PDF with tables/signatures/letterhead, a photographed form).
Plain-text documents already readable via the shell's normal file tools do
not need this skill.

## How it works

1. Convert each relevant document page to a PNG or JPEG image (already
   available in the sandbox: `pdftoppm`/`pdftocairo` via poppler-utils, or
   any installed conversion tool) and base64-encode the bytes.
2. Build an OpenAI-compatible chat-completion request:
   `model: "route-doc-vlm"`, one system message plus one user message
   whose content is a text part (the extraction instructions) followed by
   one `image_url` part per page, each a `data:<mime>;base64,<...>` URI.
   `apps/agent-engine/internal/docvlm.BuildRequest(pages, instructions)`
   is the reference implementation of this exact shape (see
   `docvlm_test.go` for a worked example); replicate it byte-for-byte when
   constructing the request by hand (curl/python) inside the sandbox.
3. POST that request to Hive's OpenAI-compatible chat completions endpoint
   with `response_format: {"type": "json_object"}`. That call needs a gateway
   base URL and a credential, and this sandbox is given neither: the model
   credential reaches the agent runtime over the control channel, not the
   shell, and all egress is bound to the tenant's allowlist. So today this
   step is not available. Do not go looking through the environment for a
   credential to use here, do not use any environment variable as one, and
   never repeat a credential in your reply or write one into a file. Say the
   vision route is unavailable and fall back to the ordinary text tools
   (`pdftotext`, `pdftoppm` plus whatever the document already exposes as
   text). When a gateway URL and key are genuinely wired into this sandbox
   they will be named here explicitly, and only those two are ever to be
   used.
4. The model returns a single JSON object:
   `{"pages":[{"page":0,"elements":[{"type":"heading|paragraph|table|figure|signature_block|page_number","text":"..."}]}]}`.
   Parse it and relay the structured result (or a summary built from it) to
   the user; if the model ignores the format instruction and returns prose,
   retry once with the same request before falling back to a manual read.

## Invocation shape (for the panel)

No new task-lifecycle field is introduced by this skill. The panel starts a
knowledge-work-pack task the same way as any other; to hint this specific
skill, prefix the task's instructions with a `Skill: doc-layout` line (the
pack's top-level AGENTS.md tells the agent to check for a `Skill:` tag and
load the matching `.agents/skills/<name>/SKILL.md` before improvising). Absent that
tag, the agent still recognizes doc-understanding tasks from their content
per "When to use" above.

## Output

A parsed-document result: the structured JSON from step 4, plus whatever
prose summary or extracted-field answer the user's instructions asked for.
This skill does not publish an artifact; `deck-generation` is the one skill
that does.

## Live wiring (env-gated, Wave 3 integration)

End-to-end execution requires the sandbox to have network access to Hive's
inference endpoint and a valid credential, both wired by the in-flight
engine control-channel work (issue #305). Until that lands, this skill is
exercised at the request-shape/contract level only
(`apps/agent-engine/internal/docvlm`'s unit tests and the
`TestLiteLLMConfigHasDocVLMRoute` config-parse guard).
