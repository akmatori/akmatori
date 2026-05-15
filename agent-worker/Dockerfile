# Dockerfile for akmatori-agent worker
# This container runs:
# - Node.js agent worker using @earendil-works/pi-coding-agent SDK
# - Connects to API via WebSocket for incident orchestration
# - Calls MCP Gateway over HTTP for tool execution (SSH, Zabbix)
#
# Security: This container has NO database access and NO direct secrets.
# Tool credentials are fetched by MCP Gateway at execution time.
# LLM API keys are passed via WebSocket from API.

# Build stage
FROM node:22-bookworm AS builder

WORKDIR /app

# Copy package files first for layer caching.
# Build context is the repo root so we can also pull akmatori_data/agents/
# into the runtime stage; that means every path here is repo-rooted.
COPY agent-worker/package.json agent-worker/package-lock.json ./

# Install all dependencies (including devDependencies for build)
RUN npm ci

# Copy source code
COPY agent-worker/tsconfig.json ./
COPY agent-worker/src/ ./src/

# Build TypeScript
RUN npm run build

# Runtime stage
FROM node:22-bookworm

# Install system dependencies needed by pi-mono's bash tool and the
# subagent search workflow (ripgrep + fzf back the runbook/memory recon
# subagents that replaced the QMD sidecar).
RUN apt-get update && apt-get install -y --no-install-recommends \
    ripgrep \
    fzf \
    git \
    jq \
    ca-certificates \
    && apt-get clean \
    && rm -rf /var/lib/apt/lists/*

# Create non-root user (UID 1001 to differ from API's UID 1000)
# This ensures agent cannot modify files created by API
RUN groupadd -g 1001 agent && \
    useradd -u 1001 -g agent -m -s /bin/bash agent

# Create working directories. The pi-subagents extension is materialised
# under /opt/pi-extensions/pi-subagents rather than ~/.pi/agent/extensions so
# the host bind-mount on /home/agent/.pi/agent/extensions (operator-supplied
# extensions) does not shadow it. The agent-runner passes this path via
# additionalExtensionPaths to the pi-mono resource loader.
RUN mkdir -p /home/agent/.pi /opt/pi-extensions/pi-subagents /workspaces && \
    chown -R agent:agent /home/agent /workspaces /opt/pi-extensions

WORKDIR /home/agent/app

# Copy package files and install production dependencies only
COPY agent-worker/package.json agent-worker/package-lock.json ./
RUN npm ci --omit=dev && npm cache clean --force

# Materialise the pi-subagents extension into the image. Copying the full
# package preserves package.json so pi-mono's resource loader honours its
# `pi.extensions` manifest (./src/extension/index.ts).
RUN cp -r node_modules/pi-subagents/. /opt/pi-extensions/pi-subagents/ && \
    chown -R agent:agent /opt/pi-extensions

# Bake Akmatori's system-supplied subagent definitions into the pi-subagents
# builtin agents directory so they are discoverable in end-user installs that
# never populate the host-side ./akmatori_data/agents/ bind mount. The host
# mount remains as an operator-override path: user agents loaded from
# ~/.pi/agent/agents/ override these builtins by name (mergeAgentsForScope).
COPY --chown=agent:agent akmatori_data/agents/*.md /opt/pi-extensions/pi-subagents/agents/

# Copy built JavaScript from builder
COPY --from=builder /app/dist/ ./dist/

# Change ownership
RUN chown -R agent:agent /home/agent/app

# Switch to non-root user
USER agent

# Environment variables (defaults, can be overridden).
# Put the local node_modules/.bin on PATH so subagent subprocesses can spawn
# the bundled `pi` CLI without an absolute path.
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
