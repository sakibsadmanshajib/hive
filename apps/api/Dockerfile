FROM node:24-alpine AS base
WORKDIR /app

FROM base AS deps
COPY package.json pnpm-workspace.yaml tsconfig.base.json ./
COPY apps/api/package.json ./apps/api/package.json
COPY apps/web/package.json ./apps/web/package.json
COPY packages/shared/package.json ./packages/shared/package.json
RUN npm install -g pnpm@9.12.3 && pnpm install --frozen-lockfile || pnpm install

FROM deps AS builder
COPY . .
RUN pnpm --filter @bd-ai-gateway/api build

FROM deps AS runner
WORKDIR /app
ENV NODE_ENV=production
COPY --from=builder /app /app
EXPOSE 8080
CMD ["pnpm", "--filter", "@bd-ai-gateway/api", "start"]
