# Technology Stack

**Analysis Date:** 2026-03-16

## Languages

**Primary:**
- TypeScript 5.7.2 - Used across all packages (API, web, shared)
- JavaScript - Generated output from TypeScript compilation

**Secondary:**
- Shell/Bash - Build scripts and tooling (`tools/dev/` scripts)

## Runtime

**Environment:**
- Node.js (version managed via pnpm)
- Browser (React 19.0.0 for web)

**Package Manager:**
- pnpm 9.12.3 - Monorepo package manager
- Lockfile: `pnpm-lock.yaml` (present)

## Frameworks

**Core:**
- Next.js 15.1.1 - Web framework (`apps/web`)
- Fastify 5.1.0 - HTTP server framework (`apps/api`)
- React 19.0.0 - UI library (`apps/web`)
- React DOM 19.0.0 - React rendering

**Testing:**
- Vitest 2.1.8 - Unit test framework (configured in `apps/web/vitest.config.ts`)
- Playwright 1.58.2 - End-to-end testing (`@playwright/test`)
- Testing Library React 16.3.0 - Component testing utilities
- Jest DOM 6.8.0 - DOM assertion utilities

**Build/Dev:**
- TypeScript 5.7.2 - Type checking and compilation
- ESLint 9.17.0 - Linting
- ESLint TypeScript Plugin 8.18.1 - TypeScript linting rules
- tsx 4.19.2 - TypeScript executor for development
- Autoprefixer 10.4.24 - CSS vendor prefixes
- PostCSS 8.5.6 - CSS processing

**UI/Styling:**
- Tailwind CSS 3.4.19 - Utility-first CSS framework (`apps/web`)
- Radix UI - Component library (`@radix-ui/react-*` packages)
- Class Variance Authority 0.7.1 - Component variant management
- Lucide React 0.542.0 - Icon library

## Key Dependencies

**Critical:**
- `@supabase/supabase-js` 2.57.4 - Backend database and authentication client (`apps/api`, `apps/web`)
- `@supabase/ssr` 0.9.0 - Supabase SSR utilities (`apps/web`)
- `fastify` 5.1.0 - Core API server (`apps/api`)
- `@fastify/cors` 11.2.0 - CORS middleware for Fastify
- `ioredis` 5.4.2 - Redis client for caching and sessions (`apps/api`)
- `pg` 8.13.1 - PostgreSQL driver (`apps/api`)

**Infrastructure:**
- `dotenv` 16.4.7 - Environment variable management (`apps/api`)
- `prom-client` 15.1.3 - Prometheus metrics (`apps/api`)

**Content & Rendering:**
- `react-markdown` 10.1.0 - Markdown rendering in React
- `remark-gfm` 4.0.1 - GitHub Flavored Markdown support
- `sonner` 2.0.7 - Toast notifications

**Utilities:**
- `clsx` 2.1.1 - Conditional className utility
- `tailwind-merge` 3.3.1 - Tailwind CSS class merging
- `jsdom` 26.1.0 - DOM emulation for testing

## Configuration

**Environment:**
- Configuration loaded from `.env` file via `dotenv` in `apps/api/src/config/env.ts`
- Key required variables:
  - `POSTGRES_URL` - PostgreSQL connection string
  - `REDIS_URL` - Redis connection string
  - `SUPABASE_URL` - Supabase project URL
  - `SUPABASE_SERVICE_ROLE_KEY` - Supabase admin key
  - Provider API keys (OpenRouter, Groq, OpenAI, Gemini, Anthropic, Ollama)
  - Payment provider secrets (Bkash, SSLCommerz)
  - Google OAuth credentials

**Build:**
- `tsconfig.json` files in each package (`apps/api/tsconfig.json`, `apps/web/tsconfig.json`, `packages/shared/tsconfig.json`)
- Base TypeScript config: `tsconfig.base.json` (extends in subpackages)
- `next.config.js` for Next.js configuration (`apps/web`)
- `vitest.config.ts` for test configuration (`apps/web`)

**Docker:**
- `docker-compose.yml` - Multi-service orchestration (API, web, Redis, Langfuse, PostgreSQL)
- `docker-compose.dev.yml` - Development overrides
- `Dockerfile` in `apps/api/` and `apps/web/` for containerization

## Platform Requirements

**Development:**
- Node.js (via pnpm)
- Docker & Docker Compose (for local stack with databases)
- PostgreSQL (via Docker)
- Redis (via Docker)
- Supabase CLI (`npx supabase`) for local backend
- Git

**Production:**
- Node.js runtime
- PostgreSQL database
- Redis instance
- External API keys:
  - Supabase cloud instance
  - AI provider credentials (OpenRouter, Groq, OpenAI, Gemini, Anthropic)
  - Payment processor credentials (Bkash, SSLCommerz)
  - Google OAuth credentials
  - Langfuse (optional observability)

---

*Stack analysis: 2026-03-16*
