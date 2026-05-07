FROM node:22-slim

# Install curl (health checks) and git (clone source)
RUN apt-get update && apt-get install -y --no-install-recommends curl git ca-certificates && \
    rm -rf /var/lib/apt/lists/*

# Clone QMD source at pinned version, install + build
ARG QMD_VERSION=v2.1.0
RUN git clone --branch ${QMD_VERSION} --depth 1 https://github.com/tobi/qmd.git /opt/qmd
WORKDIR /opt/qmd
# Remove bun lock files so bin/qmd's runtime detection picks node (not bun)
RUN rm -f bun.lock bun.lockb && npm install && npm run build && npm link

# Patch MCP server to respect QMD_BIND_HOST env var (default: localhost)
# Upstream hardcodes "localhost" — we need 0.0.0.0 for Docker networking
RUN sed -i 's/httpServer.listen(port, "localhost"/httpServer.listen(port, process.env.QMD_BIND_HOST || "localhost"/' dist/mcp/server.js && \
    # Default LLM reranking to OFF — there is no local LLM in this container.
    # MCP `query` tool now exposes `rerank` natively (v2.1.0), so we just flip
    # the schema default from true to false. Callers can still opt in.
    sed -i 's/rerank: z\.boolean()\.optional()\.default(true)/rerank: z.boolean().optional().default(false)/' dist/mcp/server.js && \
    # The REST /query endpoint doesn't pass `rerank` to store.search at all in
    # v2.1.0 — inject `rerank: false` so it matches MCP behavior.
    sed -i 's/intent: params\.intent,$/intent: params.intent, rerank: false,/' dist/mcp/server.js && \
    # Verify all three patches applied (sed silently no-ops on unmatched
    # patterns; future QMD upgrades will fail loudly here if anchors drift).
    grep -q 'process.env.QMD_BIND_HOST' dist/mcp/server.js && \
    grep -q 'rerank: z\.boolean()\.optional()\.default(false)' dist/mcp/server.js && \
    grep -q 'intent: params.intent, rerank: false' dist/mcp/server.js

# Add a POST /update route to the MCP HTTP server. The akmatori API server
# POSTs http://qmd:8181/update after every runbook CRUD; without this route
# the call 404s and the search index drifts from disk.
COPY patch-server.js /tmp/patch-server.js
RUN node /tmp/patch-server.js /opt/qmd/dist/mcp/server.js

# Copy config and entrypoint
COPY qmd-config.yml /opt/qmd-config.yml
COPY entrypoint.sh /opt/entrypoint.sh
RUN chmod +x /opt/entrypoint.sh

# QMD MCP HTTP server port
EXPOSE 8181

ENTRYPOINT ["/opt/entrypoint.sh"]
