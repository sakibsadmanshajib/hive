# Phase 11 Research: Real OpenAI SDK Regression Tests

## Summary
Phase 11 aims to elevate the project's testing quality by implementing comprehensive, CI-style end-to-end (E2E) tests. These tests will use the official OpenAI Node.js SDK as the primary HTTP client to interact with a real Fastify instance of the API. By using the official SDK, we ensure that the API's response formats are fully compliant with OpenAI's expectations, as the SDK itself performs strict parsing and validation.

## Coverage Analysis (existing gaps)
Currently, `apps/api/test/openai-sdk-regression.test.ts` only contains 5 basic tests:
1. `models.list()` with invalid key (public endpoint check)
2. `chat.completions.create()` with invalid key (auth error check)
3. `POST /v1/audio/speech` returns 404 (stub check using `fetch`)
4. `GET /v1/files` returns 404 (stub check using `fetch`)
5. `POST /v1/chat/completions` with invalid key returns 401 (auth check using `fetch`)

**Identified Gaps:**
- **Success Paths:** No successful E2E flows exist for Chat, Embeddings, Images, or the Responses API.
- **Streaming:** Streaming chat completions (SSE) are not tested using the SDK's async iterator.
- **Model Details:** `GET /v1/models/:id` is not tested.
- **Error States:** Status codes like 400 (bad request), 402 (insufficient credits), 429 (rate limit), and 502 (provider error) lack SDK-level verification.
- **Custom Endpoints:** The project-specific `/v1/responses` endpoint needs SDK integration tests.

## Test Organization Strategy
- **Single Suite File:** Given the project structure, expanding `apps/api/test/openai-sdk-regression.test.ts` into a comprehensive suite is appropriate. If it exceeds ~500 lines, it should be split by domain (e.g., `openai-sdk-chat.test.ts`, `openai-sdk-models.test.ts`).
- **Helper Expansion:** `apps/api/test/helpers/test-app.ts` needs to be significantly enhanced to support more complex mock scenarios (e.g., toggling rate limits, simulating credit exhaustion).
- **Isolation:** Tests must remain isolated by using `createTestApp` for each test or `describe` block to prevent state leakage between runs.

## SDK Method → Endpoint Mapping
| OpenAI SDK Method | API Endpoint | Description |
|-------------------|--------------|-------------|
| `client.models.list()` | `GET /v1/models` | Lists all available models |
| `client.models.retrieve(id)` | `GET /v1/models/:id` | Gets details for a specific model |
| `client.chat.completions.create({ stream: false })` | `POST /v1/chat/completions` | Standard non-streaming chat |
| `client.chat.completions.create({ stream: true })` | `POST /v1/chat/completions` | Streaming chat (SSE) |
| `client.embeddings.create()` | `POST /v1/embeddings` | Vector embedding generation |
| `client.images.generate()` | `POST /v1/images/generations` | Image generation (DALL-E style) |
| `client.responses.create()` | `POST /v1/responses` | Project-specific Responses API |

## Key Test Scenarios (per endpoint)
### Models
- **List:** Verify it returns a `list` object with an array of models.
- **Retrieve:** Verify valid model returns 200; invalid model returns 404 `model_not_found`.

### Chat Completions
- **Non-streaming:** Verify 200 OK, check `choices[0].message.content`.
- **Streaming:** Use `for await (const chunk of stream)` to verify all chunks arrive and the final chunk contains the completion.
- **Insufficient Credits:** Verify 402 status throws the correct SDK error.

### Embeddings
- **Standard:** Verify 200 OK, check `data[0].embedding` is an array of numbers.

### Image Generation
- **Standard:** Verify 200 OK, check `data[0].url` contains a valid URL string.
- **Invalid Prompt:** Verify 400 Bad Request if prompt is missing.

### Responses API
- **Standard:** Verify 200 OK, check `output[0].content[0].text` matches expected mock.

### Global / Auth / Rate Limits
- **Invalid Key:** Verify `OpenAI.AuthenticationError`.
- **Rate Limit:** Verify 429 status code results in an SDK error (typically `RateLimitError`).
- **Stubs:** Verify all endpoints in `v1-stubs.ts` return 404 with `unsupported_endpoint`.

## Mock Service Requirements
To facilitate these tests, `MockServices` in `test-app.ts` must be expanded:
- **`users.resolveApiKey`**: Should support a "no-credits-key" to trigger 402.
- **`rateLimiter.allow`**: Should be controllable via a flag to trigger 429.
- **`ai.chatCompletions`**: Should return realistic mock `ChatCompletion` objects.
- **`ai.embeddings`**: Should return realistic mock `Embedding` objects.
- **`ai.imageGeneration`**: Should return realistic mock `ImagesResponse` objects.

## Implementation Approach
1. **Refactor `MockServices`**: Update the type and the `createMockServices` helper to allow more granular control over mock behavior.
2. **Setup Test App**: Use `beforeAll` to spin up the Fastify instance on a random port.
3. **Iterative Test Implementation**:
   - Implement Model tests (simplest).
   - Implement Chat tests (non-streaming, then streaming).
   - Implement Embeddings and Images.
   - Implement Responses API.
   - Implement Error and Edge cases.
4. **Validation**: Run all tests with `vitest` and ensure no "unsafe casts" or `any` types are introduced, adhering to strict TypeScript mode.

## Validation Architecture
- **Vitest**: The existing test runner.
- **OpenAI Node SDK**: The source of truth for "Real" E2E behavior.
- **Fastify inject/listen**: Using `app.listen(0)` ensures we are testing the full HTTP stack (network, serialization, hooks).

## Risks & Considerations
- **Streaming Complexity**: Testing async iterators in Vitest requires careful handling of timeouts and completion.
- **Mock Accuracy**: If the mock service doesn't return *exactly* what the OpenAI SDK expects, the SDK will throw parsing errors. This is actually a *feature* of Phase 11, as it forces us to fix any compliance bugs in the API.
- **SDK Versioning**: The project uses `openai: ^4.x`. We should ensure our implementation matches the version installed in `node_modules`.
- **Responses API**: Since this is a newer/custom implementation, we must verify the SDK version supports `client.responses.create()`. (Research confirmed it is present in `openai/resources/responses`).
