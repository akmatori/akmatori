#!/bin/sh
set -e

# Set up QMD config directory
mkdir -p /root/.config/qmd

# Copy collection config
cp /opt/qmd-config.yml /root/.config/qmd/index.yml

# Scan files and build index (BM25 lexical search)
echo "QMD: Scanning runbook files..."
qmd update || echo "QMD: No files to index yet (runbooks directory may be empty)"

# Note: Embedding (qmd embed) is skipped in Docker by default.
# BM25 lexical search works without embeddings.
# To enable vector search, configure an embedding provider and run qmd embed.

# Start MCP HTTP server
echo "QMD: Starting MCP HTTP server on port 8181..."
exec qmd mcp --http --port 8181
