# ---- STAGE 1: Defining 'source' ----
# Here we tell Docker: "Create a temporary stage named 'source'".
FROM alpine/git:latest AS source
# ARG REPO_URL="https://github.com/saleor/storefront.git"
# ARG REPO_BRANCH="main"
WORKDIR /app
RUN git clone --single-branch --branch main https://github.com/saleor/storefront.git .

# ---- STAGE 2: Base Image ----
FROM node:24.12.0-alpine3.23 AS base 

# ---- STAGE 3: Dependencies ----
# Here we tell Docker: "Create a temporary stage named 'deps'".
FROM base AS deps
RUN apk add --no-cache libc6-compat
WORKDIR /app

ENV PNPM_HOME="/pnpm"
ENV PATH="$PNPM_HOME:$PATH"
RUN corepack enable

# Here we use 'source' as the file source
COPY --from=source /app/package.json /app/pnpm-lock.yaml ./
RUN pnpm i --frozen-lockfile --prefer-offline

# ---- STAGE 4: Application Build (Builder) ----
FROM base AS builder
WORKDIR /app

# Here we use 'deps' and 'source' as file sources
COPY --from=deps /app/node_modules ./node_modules
COPY --from=source /app .

ENV NEXT_OUTPUT=standalone
ARG NODE_TLS_REJECT_UNAUTHORIZED
ARG NEXT_PUBLIC_SALEOR_API_URL
ARG NEXT_PUBLIC_STOREFRONT_URL
ARG NEXT_PUBLIC_DEFAULT_CHANNEL
ENV NEXT_PUBLIC_SALEOR_API_URL=${NEXT_PUBLIC_SALEOR_API_URL}
ENV NEXT_PUBLIC_STOREFRONT_URL=${NEXT_PUBLIC_STOREFRONT_URL}
ENV NEXT_PUBLIC_DEFAULT_CHANNEL=${NEXT_PUBLIC_DEFAULT_CHANNEL}

ENV PNPM_HOME="/pnpm"
ENV PATH="$PNPM_HOME:$PATH"
RUN corepack enable

RUN pnpm build

# ---- STAGE 5: Final Image (Runner) ----
FROM base AS runner
WORKDIR /app

ENV NODE_ENV=production

ARG NEXT_PUBLIC_SALEOR_API_URL
ENV NEXT_PUBLIC_SALEOR_API_URL=${NEXT_PUBLIC_SALEOR_API_URL}
ARG NEXT_PUBLIC_STOREFRONT_URL
ENV NEXT_PUBLIC_STOREFRONT_URL=${NEXT_PUBLIC_STOREFRONT_URL}

ARG NODE_TLS_REJECT_UNAUTHORIZED
ENV NODE_TLS_REJECT_UNAUTHORIZED=${NODE_TLS_REJECT_UNAUTHORIZED}

RUN addgroup --system --gid 1001 nodejs
RUN adduser --system --uid 1001 nextjs

# Set the correct permission for prerender cache
RUN mkdir .next
RUN chown nextjs:nodejs .next


# # Here we use 'builder' as the file source
COPY --from=builder --chown=nextjs:nodejs /app/public ./public
COPY --from=builder --chown=nextjs:nodejs /app/.next/standalone ./
COPY --from=builder --chown=nextjs:nodejs /app/.next/static ./.next/static

USER nextjs

EXPOSE 3000
ENV PORT=3000

CMD ["node", "server.js"]