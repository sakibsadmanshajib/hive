# Coding Conventions

**Analysis Date:** 2026-03-16

## Naming Patterns

**Files:**
- kebab-case for file names: `chat-completions.ts`, `chat-sessions-route.test.ts`, `message-list.tsx`
- Component files use `.tsx` extension
- Test files use `.test.ts` or `.test.tsx` suffix before extension
- Route handler files end with `-route.ts` suffix in test directory

**Functions:**
- camelCase for function names: `registerChatCompletionsRoute()`, `createReply()`, `formatTimestamp()`
- Exported functions use verb-noun pattern: `registerUserRoutes()`, `requireApiPrincipal()`, `inferUsageChannel()`
- Helper functions use verb pattern: `readCreateApiKeyBody()`, `createServices()`

**Variables:**
- camelCase for local variables and parameters: `statusCode`, `sentPayload`, `errorMessage`
- const-first pattern: prioritize `const` over `let` and `var`
- Prefix private/internal helpers with leading underscore when needed for parameter ignoring: `argsIgnorePattern: "^_"` in ESLint

**Types:**
- PascalCase for type/interface names: `ChatBody`, `MessageItem`, `MessageListProps`, `RuntimeServices`, `FastifyInstance`
- Type definitions appear at top of file near imports
- Function parameter types use `type Parameter = { ... }` pattern when inline
- Discriminated unions for error handling: `if ("error" in result) { ... }`

## Code Style

**Formatting:**
- ESLint + TypeScript strict mode enforced
- Target ES2022 with ESNext module resolution
- `skipLibCheck: true` in TypeScript config to avoid type-checking dependencies
- No explicit formatter configuration file detected (Prettier not in use)

**Linting:**
- ESLint 9.17.0 with TypeScript plugin (`@typescript-eslint/eslint-plugin@8.18.1`)
- Rule: `@typescript-eslint/no-explicit-any` is OFF (allows `any` type)
- Rule: `@typescript-eslint/no-unused-vars` warns on unused variables EXCEPT those prefixed with underscore (`_`): `argsIgnorePattern: "^_"`, `varsIgnorePattern: "^_"`
- Next.js core web vitals rules applied to `apps/web/**/*` files via `eslint-config-next`

## Import Organization

**Order:**
1. Third-party type imports: `import type { FastifyInstance } from "fastify"`
2. Third-party default/namespace imports: `import { describe, expect, it, vi } from "vitest"`
3. Local relative imports with explicit paths: `import { registerChatCompletionsRoute } from "../../src/routes/chat-completions"`
4. Relative imports for components and utilities: `import { AuthModal } from "../features/auth/components/auth-modal"`

**Path Aliases:**
- Not detected in tsconfig - relative paths used throughout
- Monorepo structure uses app-prefixed package names: `@hive/api`, `@hive/web`, `@hive/shared`

## Error Handling

**Patterns:**
- Early return strategy: handlers check conditions and return early if validation fails
- Discriminated unions for success/error: `if ("error" in result) { return reply.code(result.statusCode).send({ error: result.error }); }`
- HTTP status codes in error responses: `reply.code(404).send()`, `reply.code(429).send()`, `reply.code(400).send()`
- Null coalescing for optional body: `request.body?.messages ?? []` (defaults to empty array)
- Guard clauses: `if (!principal) { return; }` to exit early before processing

**API Response Format:**
- Success: return object directly with typed fields
- Error: return `{ error: "message" }` with appropriate HTTP status code
- Headers passed via reply chain: `reply.header("x-key", value).code(statusCode).send(payload)`

## Logging

**Framework:** console (built-in) or none detected; async logging via services pattern

**Patterns:**
- No explicit logging framework imported in analyzed files
- Services injected at runtime: `services.ai`, `services.rateLimiter`, `services.users`
- Structured logging implied through service layer abstraction

## Comments

**When to Comment:**
- Type annotations preferred over comments for clarity
- JSDoc/TSDoc not enforced; none found in analyzed samples
- Inline comments minimal; code structure speaks for itself

**JSDoc/TSDoc:**
- Not observed in codebase; type annotations provide documentation

## Function Design

**Size:** Functions kept focused and under 50 lines typically

**Parameters:**
- Use type-safe destructuring: `type ChatBody = { model?: string; messages?: ... }`
- Accept typed body objects: `<{ Body: ChatBody }>`
- Optional parameters use `?` syntax: `expiresAt?: string`

**Return Values:**
- Async functions return Promises explicitly typed
- Early returns preferred over nested conditionals
- Discriminated union returns for result types: `{ error: string; statusCode: number } | { headers: {...}; statusCode: 200 }`

## Module Design

**Exports:**
- Named exports for route handlers: `export function registerChatCompletionsRoute(...)`
- Register pattern: handler functions take app instance and services
- Type exports for internal contracts: `type Handler = (request?: any, reply?: any) => Promise<unknown>`

**Barrel Files:**
- Index files aggregate routes: `src/routes/index.ts` exports all route registrations

---

*Convention analysis: 2026-03-16*
