# Slipstream-Go DNS Tunnel
# Multi-stage build for minimal image size
#
# Build targets:
#   docker build --target server -t slipstream-server .
#   docker build --target client -t slipstream-client .
#   docker build -t slipstream-go .  (combined image)
#
# Run examples:
#   docker run -p 5353:5353/udp -v ./keys:/app/keys slipstream-server --domain tunnel.local --privkey-file /app/keys/server.key
#   docker run -p 1080:1080 -v ./keys:/app/keys slipstream-client --domain tunnel.local --pubkey-file /app/keys/server.pub

ARG GO_VERSION=1.25

# ============================================
# Build Stage
# ============================================
FROM golang:${GO_VERSION}-alpine AS builder

WORKDIR /build

# Install build dependencies
RUN apk add --no-cache git ca-certificates tzdata

# Copy go mod files first for better layer caching
COPY go.mod go.sum ./
RUN go mod download && go mod verify

# Copy source code
COPY . .

# Build server binary with optimizations
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
    -ldflags="-w -s -X main.version=$(git describe --tags --always 2>/dev/null || echo 'dev')" \
    -o /slipstream-server ./cmd/server

# Build client binary with optimizations
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
    -ldflags="-w -s -X main.version=$(git describe --tags --always 2>/dev/null || echo 'dev')" \
    -o /slipstream-client ./cmd/client

# ============================================
# Server Image
# ============================================
FROM alpine:3.19 AS server

LABEL maintainer="Slipstream-Go" \
      description="DNS Tunnel Server - QUIC over DNS" \
      version="1.0.0"

RUN apk add --no-cache ca-certificates tzdata

# Create non-root user
RUN adduser -D -u 1000 slipstream

WORKDIR /app

# Copy binary
COPY --from=builder /slipstream-server /usr/local/bin/slipstream-server

# Create keys directory
RUN mkdir -p /app/keys && chown -R slipstream:slipstream /app

USER slipstream

# DNS server port (UDP)
EXPOSE 5353/udp

HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
    CMD pgrep slipstream-server || exit 1

ENTRYPOINT ["slipstream-server"]
CMD ["--help"]

# ============================================
# Client Image
# ============================================
FROM alpine:3.19 AS client

LABEL maintainer="Slipstream-Go" \
      description="DNS Tunnel Client - SOCKS5 Proxy" \
      version="1.0.0"

RUN apk add --no-cache ca-certificates tzdata

# Create non-root user
RUN adduser -D -u 1000 slipstream

WORKDIR /app

# Copy binary
COPY --from=builder /slipstream-client /usr/local/bin/slipstream-client

# Create keys directory
RUN mkdir -p /app/keys && chown -R slipstream:slipstream /app

USER slipstream

# SOCKS5 proxy port
EXPOSE 1080/tcp

HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
    CMD pgrep slipstream-client || exit 1

ENTRYPOINT ["slipstream-client"]
CMD ["--help"]

# ============================================
# Combined Image (Default)
# ============================================
FROM alpine:3.19

LABEL maintainer="Slipstream-Go" \
      description="DNS Tunnel - Combined Server & Client" \
      version="1.0.0"

RUN apk add --no-cache ca-certificates tzdata

# Create non-root user
RUN adduser -D -u 1000 slipstream

WORKDIR /app

# Copy both binaries
COPY --from=builder /slipstream-server /usr/local/bin/slipstream-server
COPY --from=builder /slipstream-client /usr/local/bin/slipstream-client

# Create keys directory
RUN mkdir -p /app/keys && chown -R slipstream:slipstream /app

USER slipstream

# Expose both ports
EXPOSE 5353/udp 1080/tcp

# Show usage by default
CMD ["sh", "-c", "echo 'Slipstream-Go DNS Tunnel'; echo ''; echo 'Usage:'; echo '  slipstream-server [options]  - Run DNS tunnel server'; echo '  slipstream-client [options]  - Run SOCKS5 client'; echo ''; echo 'Run with --help for options'"]
