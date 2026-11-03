# Dockerfile for akmatori-agent worker
# This container runs:
# - Node.js agent worker using @earendil-works/pi-coding-agent SDK
# - Connects to API via WebSocket for incident orchestration
# - Calls MCP Gateway over HTTP for tool execution (SSH, Zabbix)
#
# Security: This container has NO database access and NO direct secrets.
# Tool credentials are fetched by MCP Gateway at execution time.
# LLM API keys are passed via WebSocket from API.

# Build stage. Node is pinned to a minor: pi >= 1.3.0 needs >= 22.19.0, and a
# floating `22` tag would change the runtime under us on every rebuild.
FROM node:22.23-bookworm AS builder

WORKDIR /app

# Copy package files first for layer caching.
# Build context is the repo root (docker-compose.dev.yml), so every path here
# is repo-rooted.
COPY agent-worker/package.json agent-worker/package-lock.json ./

# Install all dependencies (including devDependencies for build)
RUN npm ci

# Copy source code
COPY agent-worker/tsconfig.json ./
COPY agent-worker/src/ ./src/

# Build TypeScript
RUN npm run build

# Runtime stage
FROM node:22.23-bookworm

# Install system dependencies needed by pi-mono's tools. ripgrep backs
# pi-mono's `grep` tool. fd-find provides the `fdfind`
# binary that pi-mono's `find` tool spawns (it accepts both `fd` and
# `fdfind` via systemBinaryNames); without it pi-mono falls back to
# downloading fd from GitHub at runtime, which fails in air-gapped
# deployments.
RUN apt-get update && apt-get install -y --no-install-recommends \
    ripgrep \
    fd-find \
    git \
    jq \
    ca-certificates \
    && apt-get clean \
    && rm -rf /var/lib/apt/lists/*

# Create non-root user (UID 1001 to differ from API's UID 1000)
# This ensures agent cannot modify files created by API
RUN groupadd -g 1001 agent && \
    useradd -u 1001 -g agent -m -s /bin/bash agent

# Create working directories.
#
# ~/.pi/agent (pi's agentDir) must exist in the image and belong to `agent`.
# Compose bind-mounts extensions/ INTO it on top of the agent_sessions volume;
# when the image lacks the directory, Docker creates it as root on a fresh
# volume, and pi (which keeps its settings and credential store there) then
# fails with EACCES.
RUN mkdir -p /home/agent/.pi/agent/extensions /workspaces && \
    chown -R agent:agent /home/agent /workspaces

WORKDIR /home/agent/app

# Copy package files and install production dependencies only
COPY agent-worker/package.json agent-worker/package-lock.json ./
RUN npm ci --omit=dev && npm cache clean --force

# Copy built JavaScript from builder
COPY --from=builder /app/dist/ ./dist/

# Change ownership
RUN chown -R agent:agent /home/agent/app

# Switch to non-root user
USER agent

# Environment variables (defaults, can be overridden).
# Put the local node_modules/.bin on PATH so the bundled `pi` CLI runs without
# an absolute path (for example `pi auth check` when diagnosing a provider).
ENV PATH=/home/agent/app/node_modules/.bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin
ENV API_WS_URL=ws://akmatori-api:3000/ws/agent
ENV MCP_GATEWAY_URL=http://mcp-gateway:8080
ENV WORKSPACE_DIR=/workspaces
ENV NODE_ENV=production

# Expose nothing - this container only makes outbound connections

# Health check - verify node process is running
HEALTHCHECK --interval=30s --timeout=3s --start-period=10s --retries=3 \
    CMD pgrep node || exit 1

# Run the agent worker
CMD ["node", "dist/index.js"]
