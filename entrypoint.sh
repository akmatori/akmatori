#!/bin/bash
set -e

# OpenAI API key is now configured via Web UI (Settings > OpenAI)
# and passed per-execution by the executor
echo "Starting AIOps Codex Bot..."
echo "Configure OpenAI API key via Settings > OpenAI in the Web UI"

# Execute the main application
exec "$@"
