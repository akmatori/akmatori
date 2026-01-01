# Build stage
FROM golang:1.25 AS builder

# Set working directory
WORKDIR /app

# Copy go mod files
COPY go.mod go.sum ./

# Download dependencies
RUN go mod download

# Copy source code
COPY . .

# Build the application
RUN CGO_ENABLED=0 GOOS=linux go build -a -ldflags="-s -w" -o akmatori ./cmd/akmatori

# Runtime stage
FROM golang:1.25

# Install Node.js, npm, Python 3, and dependencies
RUN apt-get update && apt-get install -y \
    curl \
    ca-certificates \
    python3 \
    python3-pip \
    python3-venv \
    ripgrep \
    httpie \
    zoxide \
    eza \
    bat \
    fzf \
    fdclone \
    git \
    jq \
    tree \
    git-delta \
    && curl -fsSL https://deb.nodesource.com/setup_20.x | bash - \
    && apt-get install -y nodejs \
    && apt-get clean \
    && rm -rf /var/lib/apt/lists/*

# Create symlink for python command (python3 -> python)
RUN ln -s /usr/bin/python3 /usr/bin/python

# Install Python packages required by skills and tools
# Tool-specific dependencies:
#   - paramiko: SSH tool (remote command execution)
RUN pip3 install --no-cache-dir --break-system-packages \
    requests \
    pyyaml \
    python-dotenv \
    PyPDF2 \
    paramiko>=3.3.0

# Create non-root user first
RUN groupadd -g 1000 akmatori && \
    useradd -u 1000 -g akmatori -m -s /bin/bash akmatori

# Install Codex CLI globally (pinned version)
RUN npm install -g @openai/codex@0.77.0

# Create data directories and fix all permissions
# Note: Codex CLI may create files in .codex during installation
RUN mkdir -p /akmatori /home/akmatori/.codex && \
    chown -R akmatori:akmatori /akmatori /home/akmatori && \
    chmod -R 755 /home/akmatori/.codex

# Set working directory
WORKDIR /home/akmatori

# Copy entrypoint script and make it executable for all users
COPY entrypoint.sh /usr/local/bin/entrypoint.sh
RUN chmod 755 /usr/local/bin/entrypoint.sh

# Copy binary from builder
COPY --from=builder /app/akmatori .

# Copy tools directory from builder
COPY --from=builder /app/tools ./tools

# Change ownership
RUN chown -R akmatori:akmatori /home/akmatori

# Switch to non-root user
USER akmatori

# Expose port
EXPOSE 3000

# Health check
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
    CMD pgrep akmatori || exit 1

# Set entrypoint to handle Codex authentication
ENTRYPOINT ["/usr/local/bin/entrypoint.sh"]

# Run the application
CMD ["./akmatori"]
