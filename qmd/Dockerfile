FROM node:22-slim

# Install curl (health checks) and git (clone source)
RUN apt-get update && apt-get install -y --no-install-recommends curl git && \
    rm -rf /var/lib/apt/lists/*

# Clone QMD source at pinned version, install + build
ARG QMD_VERSION=v2.0.1
RUN git clone --branch ${QMD_VERSION} --depth 1 https://github.com/tobi/qmd.git /opt/qmd
WORKDIR /opt/qmd
RUN npm install && npm run build && npm link

# Copy config and entrypoint
COPY qmd-config.yml /opt/qmd-config.yml
COPY entrypoint.sh /opt/entrypoint.sh
RUN chmod +x /opt/entrypoint.sh

# QMD MCP HTTP server port
EXPOSE 8181

ENTRYPOINT ["/opt/entrypoint.sh"]
