FROM node:22-slim

# Install curl (health checks) and git (clone source)
RUN apt-get update && apt-get install -y --no-install-recommends curl git ca-certificates && \
    rm -rf /var/lib/apt/lists/*

# Clone QMD source at pinned version, install + build
ARG QMD_VERSION=v2.0.1
RUN git clone --branch ${QMD_VERSION} --depth 1 https://github.com/tobi/qmd.git /opt/qmd
WORKDIR /opt/qmd
# Remove bun lock files so bin/qmd's runtime detection picks node (not bun)
RUN rm -f bun.lock bun.lockb && npm install && npm run build && npm link

# Patch MCP server to respect QMD_BIND_HOST env var (default: localhost)
# Upstream hardcodes "localhost" — we need 0.0.0.0 for Docker networking
RUN sed -i 's/httpServer.listen(port, "localhost"/httpServer.listen(port, process.env.QMD_BIND_HOST || "localhost"/' dist/mcp/server.js && \
    # Disable LLM reranking in MCP search — no local LLM available in Docker.
    # BM25 (lex) + RRF scoring is sufficient for runbook search.
    sed -i 's/intent,$/intent, rerank: false,/;s/intent: params\.intent,$/intent: params.intent, rerank: false,/' dist/mcp/server.js

# Copy config and entrypoint
COPY qmd-config.yml /opt/qmd-config.yml
COPY entrypoint.sh /opt/entrypoint.sh
RUN chmod +x /opt/entrypoint.sh

# QMD MCP HTTP server port
EXPOSE 8181

ENTRYPOINT ["/opt/entrypoint.sh"]
