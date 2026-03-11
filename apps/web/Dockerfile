FROM node:24-alpine AS base
WORKDIR /app

FROM base AS deps
COPY package.json pnpm-workspace.yaml tsconfig.base.json ./
COPY apps/web/package.json ./apps/web/package.json
COPY apps/api/package.json ./apps/api/package.json
COPY packages/shared/package.json ./packages/shared/package.json
RUN npm install -g pnpm@9.12.3 && pnpm install --frozen-lockfile || pnpm install

FROM deps AS builder
ARG NEXT_PUBLIC_API_BASE_URL=http://127.0.0.1:8080
ARG NEXT_PUBLIC_SUPABASE_URL=http://127.0.0.1:54321
ARG NEXT_PUBLIC_SUPABASE_ANON_KEY=test-supabase-anon-key
ENV NEXT_PUBLIC_API_BASE_URL=$NEXT_PUBLIC_API_BASE_URL
ENV NEXT_PUBLIC_SUPABASE_URL=$NEXT_PUBLIC_SUPABASE_URL
ENV NEXT_PUBLIC_SUPABASE_ANON_KEY=$NEXT_PUBLIC_SUPABASE_ANON_KEY
COPY . .
RUN pnpm --filter @hive/web build

FROM deps AS runner
WORKDIR /app
ENV NODE_ENV=production
COPY --from=builder /app /app
EXPOSE 3000
CMD ["pnpm", "--filter", "@hive/web", "start"]
