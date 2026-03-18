FROM node:22-slim

# Install curl for health checks
RUN apt-get update && apt-get install -y --no-install-recommends curl && \
    rm -rf /var/lib/apt/lists/*

# Copy QMD source from additional build context and install + build
COPY --from=qmd-src / /opt/qmd
WORKDIR /opt/qmd
RUN npm install && npm run build && npm link

# Copy config and entrypoint
COPY qmd-config.yml /opt/qmd-config.yml
COPY entrypoint.sh /opt/entrypoint.sh
RUN chmod +x /opt/entrypoint.sh

# QMD MCP HTTP server port
EXPOSE 8181

ENTRYPOINT ["/opt/entrypoint.sh"]
