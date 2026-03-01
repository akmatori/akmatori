# Build stage
FROM golang:1.24-alpine AS builder

WORKDIR /app

# Install git for go modules
RUN apk add --no-cache git

# Copy go mod files
COPY go.mod go.sum ./

# Download dependencies
RUN go mod download

# Copy source code
COPY . .

# Build the application
RUN CGO_ENABLED=0 GOOS=linux go build -o mcp-gateway ./cmd/gateway

# Runtime stage
FROM alpine:3.20

WORKDIR /app

# Install CA certificates for HTTPS
RUN apk add --no-cache ca-certificates

# Create non-root user
RUN adduser -D -u 1000 mcpgateway

# Copy binary from builder
COPY --from=builder /app/mcp-gateway /app/mcp-gateway

# Create entrypoint script that builds DATABASE_URL from password file
RUN printf '#!/bin/sh\nif [ -z "$DATABASE_URL" ]; then\n  PG_PASS=$(cat "${POSTGRES_PASSWORD_FILE:-/akmatori/secrets/postgres_password}" 2>/dev/null || echo "akmatori")\n  export DATABASE_URL="postgres://${POSTGRES_USER:-akmatori}:${PG_PASS}@${POSTGRES_HOST:-postgres}:5432/${POSTGRES_DB:-akmatori}?sslmode=disable"\nfi\nexec "$@"\n' > /app/entrypoint.sh && chmod +x /app/entrypoint.sh

# Switch to non-root user
USER mcpgateway

# Expose port
EXPOSE 8080

# Health check
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
    CMD wget --no-verbose --tries=1 --spider http://localhost:8080/health || exit 1

# Use entrypoint to build DATABASE_URL from password file
ENTRYPOINT ["/app/entrypoint.sh"]
CMD ["/app/mcp-gateway"]
