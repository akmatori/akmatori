#!/bin/sh
# Entrypoint for akmatori-api and mcp-gateway containers.
# Builds DATABASE_URL from a password file if DATABASE_URL is not already set.

if [ -z "$DATABASE_URL" ]; then
  PG_PASS=$(cat "${POSTGRES_PASSWORD_FILE:-/akmatori/secrets/postgres_password}" 2>/dev/null || echo "akmatori")
  export DATABASE_URL="postgres://${POSTGRES_USER:-akmatori}:${PG_PASS}@${POSTGRES_HOST:-postgres}:5432/${POSTGRES_DB:-akmatori}?sslmode=disable"
fi

exec "$@"
