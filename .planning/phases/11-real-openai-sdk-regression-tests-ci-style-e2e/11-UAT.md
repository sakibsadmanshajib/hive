---
status: complete
phase: 11-real-openai-sdk-regression-tests-ci-style-e2e
source: 11-01-SUMMARY.md
started: 2026-03-22T20:22:04Z
updated: 2026-03-22T20:25:43Z
---

## Current Test

[testing complete]

## Tests

### 1. OpenAI SDK Models Retrieve
expected: Using the OpenAI Node SDK against this API, retrieving a known model such as `mock-chat` should return an OpenAI-style model object for that exact id instead of throwing or failing SDK parsing.
result: pass
note: Verified by `pnpm --filter @hive/api exec vitest run test/openai-sdk-regression.test.ts` covering `models.retrieve('mock-chat')`; full API suite also passed.

### 2. OpenAI SDK Chat Completion
expected: A non-stream chat completion request through the OpenAI Node SDK should return a standard chat completion payload with a valid id, assistant message content, and no SDK parse errors.
result: pass
note: Verified by targeted regression test coverage for `chat.completions.create()` and by the full API suite.

### 3. OpenAI SDK Streaming Chat
expected: A streaming chat completion request through the OpenAI Node SDK should yield an async-iterable stream with at least two chunks and non-empty combined assistant text.
result: pass
note: Verified by targeted regression test coverage for `chat.completions.create({ stream: true })`, which received at least two chunks and non-empty combined content.

### 4. OpenAI SDK Embeddings
expected: An embeddings request through the OpenAI Node SDK should return embedding data in the expected OpenAI shape without contract or parsing failures.
result: pass
note: Verified by targeted regression test coverage for `embeddings.create()` against the runtime catalog path using `text-embedding-3-small`.

### 5. OpenAI SDK Image Generation
expected: An image generation request through the OpenAI Node SDK should return an OpenAI-style images payload without schema mismatch or SDK parse failures.
result: pass
note: Verified by targeted regression test coverage for `images.generate()` and by the full API suite.

### 6. Responses Endpoint Compatibility
expected: Posting to `/v1/responses` should return a live OpenAI-style response payload that a client can parse without contract mismatches.
result: pass
note: Verified by targeted regression test coverage for `POST /v1/responses` and by the full API suite.

### 7. Typed Error Mapping
expected: Known failure paths should surface as the correct SDK/API error types and statuses: unknown model as 404 `NotFoundError`, insufficient credits as 402 `APIError`, rate limit as 429 `RateLimitError`, and invalid request as 400 `BadRequestError`.
result: pass
note: Verified by targeted regression test coverage for 404 `NotFoundError`, 402 `APIError`, 429 `RateLimitError`, and 400 `BadRequestError`.

## Summary

total: 7
passed: 7
issues: 0
pending: 0
skipped: 0

## Gaps

[none yet]
