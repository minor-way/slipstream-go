# Slipstream-Go

A high-performance DNS tunneling solution that encapsulates QUIC traffic within DNS queries and responses. Written in Go for maximum portability and performance.

## Features

### Core Capabilities
- **QUIC over DNS** - Tunnels QUIC protocol through DNS TXT records
- **SOCKS5 Proxy** - Client provides local SOCKS5 proxy interface
- **Ed25519 Authentication** - Secure key-based server authentication with certificate pinning
- **Multi-Domain Support** - Server accepts multiple tunnel domains
- **Auto-Reconnection** - Client automatically reconnects with exponential backoff

### Performance Optimizations
- **Optimized Polling** - 10ms poll interval for low-latency responses
- **Turbo Poll** - Immediate polling after receiving data
- **Pre-fragmentation** - Server pre-fragments packets for efficient DNS responses
- **Parallel Fragment Delivery** - Multiple fragments per DNS response
- **Memory Management** - Configurable memory limits with Go's soft memory cap

### Enhancements over Rust/C Implementations

| Feature | Slipstream-Go | Rust Version | C Version |
|---------|--------------|--------------|-----------|
| Auto-reconnect | Yes | No | No |
| Multi-domain | Yes | Single | Single |
| Memory management | Soft limit | Manual | Manual |
| Build complexity | `go build` | Cargo + deps | Make + libs |
| Cross-compilation | Built-in | Requires setup | Complex |
| Binary size | ~10MB | ~5MB | ~2MB |
| Startup time | ~50ms | ~30ms | ~10ms |
| Response latency | ~1-2s | ~1-2s | ~1-2s |

## Architecture

```
┌─────────────────┐         DNS Queries          ┌─────────────────┐
│                 │  ──────────────────────────▶ │                 │
│  Slipstream     │    (QUIC packets in TXT)     │  Slipstream     │
│  Client         │                              │  Server         │
│                 │  ◀────────────────────────── │                 │
│  SOCKS5 Proxy   │       DNS Responses          │  DNS Server     │
│  (localhost)    │    (QUIC packets in TXT)     │                 │
└─────────────────┘                              └─────────────────┘
        │                                                │
        │ SOCKS5                                        │ Direct/SOCKS5
        ▼                                                ▼
   Applications                                    Target Servers
```

### Data Flow
1. Application connects to local SOCKS5 proxy (client)
2. Client encodes QUIC packets as Base32 in DNS query labels
3. DNS queries traverse network to server
4. Server decodes packets, forwards via QUIC to targets
5. Responses return via DNS TXT records (Base64 encoded)

## Installation

### Prerequisites
- Go 1.21 or later

### Build from Source

```bash
# Clone the repository
git clone https://github.com/minor-way/slipstream-go.git
cd slipstream-go

# Build binaries
go build -o bin/slipstream-server ./cmd/server
go build -o bin/slipstream-client ./cmd/client

# Or use the deployment script
./deploy.sh build
```

### Docker

```bash
# Build server image
docker build --target server -t slipstream-server .

# Build client image
docker build --target client -t slipstream-client .

# Build combined image
docker build -t slipstream-go .
```

## Quick Start

### 1. Generate Keys

```bash
./bin/slipstream-server --gen-key \
  --privkey-file keys/server.key \
  --pubkey-file keys/server.pub
```

### 2. Start Server

```bash
./bin/slipstream-server \
  --domain tunnel.example.com \
  --dns-port 5353 \
  --target-type direct \
  --privkey-file keys/server.key \
  --log-level info
```

### 3. Start Client

```bash
./bin/slipstream-client \
  --domain tunnel.example.com \
  --resolver YOUR_SERVER_IP:5353 \
  --listen 127.0.0.1:1080 \
  --pubkey-file keys/server.pub \
  --log-level info
```

### 4. Use the Tunnel

```bash
# Test with curl
curl -x socks5://127.0.0.1:1080 https://ifconfig.me

# Configure browser to use SOCKS5 proxy at 127.0.0.1:1080
```

## Usage

### Interactive Deployment

```bash
./deploy.sh
```

The interactive script guides you through:
- Choosing server or client deployment
- Configuring all options
- Generating keys
- Starting the service

### Server Options

```
Usage: slipstream-server [options]

Options:
  --domain string        Allowed tunnel domain (can be specified multiple times)
  --dns-port int         DNS server port (default 5353)
  --target-type string   Target type: direct or socks5 (default "direct")
  --target string        Upstream SOCKS5 address (required if target-type=socks5)
  --privkey-file string  Ed25519 private key file
  --pubkey-file string   Public key output file (with --gen-key)
  --gen-key              Generate keys and exit
  --log-level string     Log level: debug/info/warn/error (default "info")
  --memory-limit int     Memory limit in MB (default 400)
```

### Client Options

```
Usage: slipstream-client [options]

Options:
  --domain string        Tunnel domain
  --resolver string      DNS resolver address (default "127.0.0.1:5353")
  --listen string        Local SOCKS5 listen address (default "127.0.0.1:1080")
  --pubkey-file string   Server public key file
  --log-level string     Log level: debug/info/warn/error (default "info")
```

### Multi-Domain Configuration

Server can accept connections from multiple domains:

```bash
./bin/slipstream-server \
  --domain tunnel1.example.com \
  --domain tunnel2.example.com \
  --domain vpn.example.org \
  --dns-port 5353 \
  --privkey-file keys/server.key
```

Clients connecting with unregistered domains are rejected with a DNS REFUSED response.

## Docker Deployment

### Using Docker Compose

1. Create `.env` file:
```bash
DOMAIN=tunnel.example.com
DNS_PORT=5353
SOCKS_PORT=1080
TARGET_TYPE=direct
LOG_LEVEL=info
MEMORY_LIMIT=400
RESOLVER=your-server-ip:5353
```

2. Generate keys:
```bash
./deploy.sh genkey
```

3. Start services:
```bash
# Start server
docker-compose up -d server

# Start client (usually on different machine)
docker-compose up -d client
```

### Manual Docker Commands

```bash
# Server
docker run -d \
  --name slipstream-server \
  -p 5353:5353/udp \
  -v $(pwd)/keys:/app/keys:ro \
  slipstream-server \
  --domain tunnel.example.com \
  --privkey-file /app/keys/server.key

# Client
docker run -d \
  --name slipstream-client \
  -p 1080:1080 \
  -v $(pwd)/keys:/app/keys:ro \
  slipstream-client \
  --domain tunnel.example.com \
  --resolver YOUR_SERVER_IP:5353 \
  --listen 0.0.0.0:1080 \
  --pubkey-file /app/keys/server.pub
```

## Logs

### Log Levels

- **debug** - Detailed packet-level logging (development only)
- **info** - Connection events and status (recommended)
- **warn** - Warnings and rejected connections
- **error** - Errors only

### Sample Log Output

**Server startup:**
```
6:00PM INF Registered allowed domain domain=tunnel.example.com
6:00PM INF Private key loaded
6:00PM INF QUIC listener started on virtual connection
6:00PM INF Starting DNS server addr=:5353 domains=1
```

**Client connection:**
```
6:00PM INF Using server public key fingerprint=MA8SxzbX...
6:00PM INF Generated session ID session=abc12345
6:00PM INF Establishing QUIC connection over DNS domain=tunnel.example.com
6:00PM INF QUIC tunnel established
6:00PM INF SOCKS5 server listening addr=127.0.0.1:1080
```

**Connection handling:**
```
6:00PM INF New SOCKS5 connection from=127.0.0.1:52341
6:00PM INF CONNECT target=example.com:443
6:00PM INF Stream opened id=4
```

**Auto-reconnection:**
```
6:00PM WRN Connection health check failed, reconnecting...
6:00PM INF Attempting reconnection attempt=1
6:00PM INF QUIC tunnel re-established
```

**Domain rejection:**
```
6:00PM WRN Rejected query for unregistered domain domain=unknown.com query=data.sess.unknown.com.
```

## DNS Configuration

For production use, configure your authoritative DNS to delegate the tunnel subdomain:

```
; In your zone file for example.com
tunnel    IN    NS    ns-tunnel.example.com.
ns-tunnel IN    A     YOUR_SERVER_IP
```

Then run the server on port 53:
```bash
./bin/slipstream-server --domain tunnel.example.com --dns-port 53 ...
```

## Security Considerations

- **Key Management**: Keep private keys secure; distribute only public keys to clients
- **Domain Validation**: Use specific domains; server rejects unregistered domains
- **Certificate Pinning**: Client validates server certificate against public key
- **Memory Limits**: Configure appropriate limits to prevent DoS
- **Firewall**: Restrict DNS port access if possible

## Performance Tuning

### Server
- Increase `--memory-limit` for high-traffic scenarios
- Use `--target-type socks5` for additional anonymity layer
- Deploy closer to target servers to reduce latency

### Client
- Use local DNS resolver for testing
- Monitor reconnection frequency in logs
- Consider multiple clients for load distribution

## Troubleshooting

### Connection Timeout
- Verify DNS port is accessible (UDP)
- Check firewall rules
- Ensure domain is registered on server

### Slow Performance
- Check network latency to DNS server
- Enable debug logging to identify bottlenecks
- Verify no packet loss on network

### Authentication Failure
- Verify public key matches server's private key
- Check key file permissions
- Regenerate keys if corrupted

## Contributing

Contributions are welcome! Please feel free to submit issues and pull requests.

## License

This project is licensed under the MIT License - see the [LICENSE](LICENSE) file for details.

## Acknowledgments

- Inspired by DNS tunneling research and tools like iodine, dnscat2
- Built with [quic-go](https://github.com/quic-go/quic-go) for QUIC protocol
- Uses [miekg/dns](https://github.com/miekg/dns) for DNS handling
