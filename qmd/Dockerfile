FROM node:22-slim

# Install curl (health checks) and git (clone source)
RUN apt-get update && apt-get install -y --no-install-recommends curl git ca-certificates && \
    rm -rf /var/lib/apt/lists/*

# Clone QMD source at pinned version, install + build.
# Pinned to a main-branch SHA rather than a tag because the hyphen-validation
# fix (d531211) and hybrid-RRF weighting fix (004714a) are not yet in any
# tagged release.
ARG QMD_VERSION=746beedb4863524d337332109dc624a0be0b5aa7
RUN git init /opt/qmd && \
    cd /opt/qmd && \
    git remote add origin https://github.com/tobi/qmd.git && \
    git fetch --depth 1 origin ${QMD_VERSION} && \
    git checkout FETCH_HEAD
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
    # Verify both patches applied (sed silently no-ops on unmatched patterns;
    # future QMD upgrades will fail loudly here if anchors drift).
    grep -q 'process.env.QMD_BIND_HOST' dist/mcp/server.js && \
    grep -q 'rerank: z\.boolean()\.optional()\.default(false)' dist/mcp/server.js

# Pre-cache the embedding + reranker GGUFs into the image so `qmd embed` on
# first start doesn't have to fetch them at runtime (~940 MB combined).
COPY precache-models.mjs /tmp/precache-models.mjs
RUN node /tmp/precache-models.mjs && rm /tmp/precache-models.mjs

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
