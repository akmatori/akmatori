# Dockerfile for akmatori-agent worker
# This container runs:
# - Node.js agent worker using @mariozechner/pi-coding-agent SDK
# - Connects to API via WebSocket for incident orchestration
# - Calls MCP Gateway over HTTP for tool execution (SSH, Zabbix)
#
# Security: This container has NO database access and NO direct secrets.
# Tool credentials are fetched by MCP Gateway at execution time.
# LLM API keys are passed via WebSocket from API.

# Build stage
FROM node:22-bookworm AS builder

WORKDIR /app

# Copy package files first for layer caching
COPY package.json package-lock.json ./

# Install all dependencies (including devDependencies for build)
RUN npm ci

# Copy source code
COPY tsconfig.json ./
COPY src/ ./src/

# Build TypeScript
RUN npm run build

# Runtime stage
FROM node:22-bookworm

# Install system dependencies needed by pi-mono's bash tool
RUN apt-get update && apt-get install -y --no-install-recommends \
    ripgrep \
    git \
    jq \
    ca-certificates \
    && apt-get clean \
    && rm -rf /var/lib/apt/lists/*

# Create non-root user (UID 1001 to differ from API's UID 1000)
# This ensures agent cannot modify files created by API
RUN groupadd -g 1001 agent && \
    useradd -u 1001 -g agent -m -s /bin/bash agent

# Create working directories
RUN mkdir -p /home/agent/.pi /workspaces && \
    chown -R agent:agent /home/agent /workspaces

WORKDIR /home/agent/app

# Copy package files and install production dependencies only
COPY package.json package-lock.json ./
RUN npm ci --omit=dev && npm cache clean --force

# Copy built JavaScript from builder
COPY --from=builder /app/dist/ ./dist/

# Change ownership
RUN chown -R agent:agent /home/agent/app

# Switch to non-root user
USER agent

# Environment variables (defaults, can be overridden)
ENV API_WS_URL=ws://akmatori-api:3000/ws/codex
ENV MCP_GATEWAY_URL=http://mcp-gateway:8080
ENV WORKSPACE_DIR=/workspaces
ENV NODE_ENV=production

# Expose nothing - this container only makes outbound connections

# Health check - verify node process is running
HEALTHCHECK --interval=30s --timeout=3s --start-period=10s --retries=3 \
    CMD pgrep node || exit 1

# Run the agent worker
CMD ["node", "dist/index.js"]
