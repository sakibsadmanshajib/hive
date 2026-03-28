---
phase: 11
plan: 01
wave: 1
title: Real OpenAI SDK regression tests — CI-style e2e
depends_on: []
files_modified:
  - apps/api/test/openai-sdk-regression.test.ts
  - apps/api/test/helpers/test-app.ts
autonomous: true
requirements_addressed:
  - CI-01  # success-path coverage for all endpoints (models, chat, embeddings, images, responses)
  - CI-02  # streaming coverage via async iterator
  - CI-03  # error-path coverage (401, 402, 429, 422, 404)
  - CI-04  # strict TypeScript compliance — no any/unknown/unsafe casts
  - CI-05  # pnpm --filter @hive/api test exits 0
---

## Objective
Expand the existing 5-test OpenAI SDK regression suite into a comprehensive CI-ready test suite covering ALL implemented endpoints with both success and error paths using the real OpenAI Node.js SDK.

## must_haves
- Configurable per-endpoint mock responses in `MockServices`.
- Success path coverage for: Models (list/retrieve), Chat (stream/non-stream), Embeddings, Images, and Responses.
- Error path coverage for: 402 (Insufficient Credits), 429 (Rate Limit), and 422 (Validation).
- Streaming tests verified using the OpenAI SDK's async iterator pattern.
- Strict TypeScript: No `as any` or unsafe casts in test code or helpers.

<task id='T1' wave='1'>
<title>Expand mock services to support configurable per-endpoint responses</title>
<read_first>
- apps/api/test/helpers/test-app.ts — understand current MockServices structure
- apps/api/src/runtime/services.ts — identify required AiService method signatures
</read_first>
<action>
Update `apps/api/test/helpers/test-app.ts`:
1. Modify `MockServices` type to include an `ai` property that matches the required `RuntimeAiService` methods (chatCompletions, chatCompletionsStream, imageGeneration, embeddings, responses).
2. Update `createMockServices` to accept an optional `overrides` parameter for these AI methods.
3. Provide default "success" mock implementations that return realistic OpenAI-compatible JSON structures.

Use concrete OpenAI SDK types from `openai/resources` — NO `any`, `unknown`, or `as` casts:
```typescript
import type { ChatCompletionCreateParams, ChatCompletion } from "openai/resources/chat/completions";
import type { EmbeddingCreateParams, CreateEmbeddingResponse } from "openai/resources/embeddings";
import type { ImageGenerateParams, ImagesResponse } from "openai/resources/images";
import type { ResponseCreateParams, Response as OAIResponse } from "openai/resources/responses/responses";

ai: {
  chatCompletions: (userId: string, body: ChatCompletionCreateParams) => Promise<ChatCompletion>;
  chatCompletionsStream: (userId: string, body: ChatCompletionCreateParams) => Promise<ReadableStream<Uint8Array>>;
  imageGeneration: (userId: string, body: ImageGenerateParams) => Promise<ImagesResponse>;
  embeddings: (userId: string, body: EmbeddingCreateParams) => Promise<CreateEmbeddingResponse>;
  responses: (userId: string, body: ResponseCreateParams) => Promise<OAIResponse>;
};
```
</action>
<done>
- `MockServices` includes all 5 AI service methods with concrete SDK types (no `any`).
- `createTestApp` continues to compile without type errors when passed `MockServices`.
- `pnpm --filter @hive/api check` passes.
</done>
<verify>cd /home/sakib/hive && pnpm --filter @hive/api exec tsc --noEmit 2>&1 | grep -c error || echo "0 errors"</verify>
</task>

<task id='T2' wave='1'>
<title>Add success-path tests for all endpoints</title>
<read_first>
- apps/api/test/openai-sdk-regression.test.ts — see existing tests
- apps/api/test/helpers/test-app.ts — T1 must complete first; use updated MockServices with overrides
</read_first>
<action>
Add the following tests to `apps/api/test/openai-sdk-regression.test.ts` using the `OpenAI` SDK client:
1. `client.models.retrieve("mock-chat")`: Verify it returns the correct model object.
2. `client.chat.completions.create({ model: "mock-chat", messages: [...] })`: Verify `choices[0].message.content` matches mock.
3. `client.embeddings.create({ model: "text-embedding-3-small", input: "hello" })`: Verify it returns a list of embeddings.
4. `client.images.generate({ prompt: "a cat" })`: Verify it returns a URL or b64_json.
5. `fetch(`${baseUrl}/v1/responses`, ...)`: Since `responses` isn't in the standard SDK, use `fetch` to verify the OpenAI-like Responses API returns a valid response object.
6. `client.models.retrieve("nonexistent-model-id")`: Verify SDK throws `OpenAI.NotFoundError` (404) with `error.code === "model_not_found"` or similar error shape.
</action>
<done>
- All 6 success/404-path tests pass (5 success + 1 model_not_found).
- Response payloads match expected OpenAI schemas (id prefixes like `chatcmpl-`, `img-`, etc.).
</done>
<verify>cd /home/sakib/hive && pnpm --filter @hive/api vitest run apps/api/test/openai-sdk-regression.test.ts 2>&1 | grep -E "✓|PASS" | head -20</verify>
</task>

<task id='T3' wave='1'>
<title>Add streaming test for chat.completions with async iterator</title>
<read_first>
- apps/api/test/openai-sdk-regression.test.ts
- apps/api/test/helpers/test-app.ts — T1 must complete first; `chatCompletionsStream` returns `Promise<ReadableStream<Uint8Array>>`
</read_first>
<action>
Implement a streaming test in `apps/api/test/openai-sdk-regression.test.ts`:
1. Call `client.chat.completions.create({ model: "mock-chat", messages: [...], stream: true })`.
2. Use `for await (const chunk of stream)` to consume chunks.
3. Verify that each `chunk.choices[0].delta.content` is a string (if present).
4. Verify the final chunk or completion state.
5. Mock `chatCompletionsStream` to return a `ReadableStream` that yields valid SSE `data: {...}` chunks.
</action>
<done>
- Streaming test passes and correctly iterates over at least 2 chunks.
- No "hanging" tests; stream closes correctly.
</done>
<verify>cd /home/sakib/hive && pnpm --filter @hive/api vitest run apps/api/test/openai-sdk-regression.test.ts 2>&1 | grep -i "stream"</verify>
</task>

<task id='T4' wave='1'>
<title>Add error-path tests (402, 429, 422)</title>
<read_first>
- apps/api/test/openai-sdk-regression.test.ts
- apps/api/test/helpers/test-app.ts — T1 must complete first; use mock overrides to trigger error responses
</read_first>
<action>
Add error-path tests that verify the SDK correctly translates HTTP errors into OpenAI error classes:
1. **402 (Insufficient Credits):** Configure mock to return 402. Verify SDK throws an error (check if it's `OpenAI.APIError` with status 402).
2. **429 (Rate Limit):** Configure mock to return 429. Verify SDK throws `OpenAI.RateLimitError`.
3. **422 (Validation):** Send an invalid request (e.g., empty messages). Verify SDK throws `OpenAI.BadRequestError` or `OpenAI.UnprocessableEntityError` (422).
</action>
<done>
- 402 test catches error and verifies status.
- 429 test catches `OpenAI.RateLimitError`.
- 422 test catches expected SDK error for validation failures.
</done>
<verify>cd /home/sakib/hive && pnpm --filter @hive/api vitest run apps/api/test/openai-sdk-regression.test.ts 2>&1 | grep -E "402|429|422|RateLimit"</verify>
</task>

<task id='T5' wave='1'>
<title>Final verification and CI alignment</title>
<files>apps/api/test/openai-sdk-regression.test.ts</files>
<read_first>
- apps/api/test/openai-sdk-regression.test.ts
</read_first>
<action>
1. Run all tests in the file: `pnpm --filter @hive/api vitest apps/api/test/openai-sdk-regression.test.ts`.
2. Ensure no regressions in existing 401/404 tests.
3. Verify that all tests are "SDK-first" where possible, using `fetch` only for non-standard or stubbed endpoints.
</action>
<done>
- All tests in `openai-sdk-regression.test.ts` pass (should be ~15+ tests total).
- Command `pnpm --filter @hive/api test` exits 0.
</done>
<verify>cd /home/sakib/hive && pnpm --filter @hive/api test 2>&1 | tail -5</verify>
</task>

## Verification
Run the regression suite:
```bash
pnpm --filter @hive/api vitest apps/api/test/openai-sdk-regression.test.ts
```
Expected: All tests pass, covering success and error paths for all major OpenAI-compatible endpoints.
