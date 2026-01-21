<h1 align="center">🌊 Slipstream-Go</h1>

<p align="center">
  <strong>High-performance DNS tunneling with QUIC protocol</strong>
</p>

<p align="center">
  <a href="#features">Features</a> •
  <a href="#quick-start">Quick Start</a> •
  <a href="#installation">Installation</a> •
  <a href="#usage">Usage</a> •
  <a href="#docker">Docker</a> •
  <a href="#license">License</a>
</p>

<p align="center">
  <img src="https://img.shields.io/badge/Go-1.21+-00ADD8?style=flat&logo=go" alt="Go Version" />
  <img src="https://img.shields.io/badge/License-MIT-green.svg" alt="License" />
  <img src="https://img.shields.io/badge/Platform-Linux%20%7C%20macOS%20%7C%20Windows-blue" alt="Platform" />
  <img src="https://img.shields.io/github/stars/minor-way/slipstream-go?style=social" alt="Stars" />
</p>

---

## What is Slipstream?

Slipstream-Go tunnels network traffic through DNS queries and responses, encapsulating the QUIC protocol within DNS TXT records. This enables secure, authenticated connections in network environments where traditional protocols are restricted.

```
┌──────────────────────────────────────────────────────────────────────────────┐
│                                                                              │
│   📱 App ──► 🔌 SOCKS5 ──► 📡 DNS Queries ──► 🖥️ Server ──► 🌐 Internet     │
│                              (QUIC in TXT)                                   │
│                                                                              │
└──────────────────────────────────────────────────────────────────────────────┘
```

---

## ✨ Features

<table>
<tr>
<td width="50%">

### 🚀 Core
- **QUIC over DNS** - Modern protocol tunneling
- **SOCKS5 Proxy** - Standard proxy interface
- **Ed25519 Auth** - Secure key-based authentication
- **Multi-Domain** - Multiple tunnel domains per server
- **Auto-Reconnect** - Exponential backoff recovery

</td>
<td width="50%">

### ⚡ Performance
- **10ms Polling** - Low-latency responses
- **Turbo Poll** - Immediate data retrieval
- **Pre-fragmentation** - Optimized packet delivery
- **Memory Limits** - Configurable soft caps
- **~1-2s Latency** - Comparable to alternatives

</td>
</tr>
</table>

---

## 📊 Comparison

| Feature | Slipstream-Go | Rust | C |
|:--------|:-------------:|:----:|:-:|
| Auto-reconnect | ✅ | ❌ | ❌ |
| Multi-domain | ✅ | ❌ | ❌ |
| Memory management | Soft limit | Manual | Manual |
| Build complexity | `go build` | Cargo | Make |
| Cross-compilation | Built-in | Setup needed | Complex |

---

## 🚀 Quick Start

### 1️⃣ Generate Keys
```bash
./bin/slipstream-server --gen-key \
  --privkey-file server.key \
  --pubkey-file server.pub
```

### 2️⃣ Start Server
```bash
./bin/slipstream-server \
  --domain tunnel.example.com \
  --dns-port 5353 \
  --privkey-file server.key
```

### 3️⃣ Start Client
```bash
./bin/slipstream-client \
  --domain tunnel.example.com \
  --resolver SERVER_IP:5353 \
  --pubkey-file server.pub
```

### 4️⃣ Use It
```bash
curl -x socks5://127.0.0.1:1080 https://ifconfig.me
```

---

## 📦 Installation

### From Source

```bash
# Clone
git clone https://github.com/minor-way/slipstream-go.git
cd slipstream-go

# Build
go build -o bin/slipstream-server ./cmd/server
go build -o bin/slipstream-client ./cmd/client
```

### Using Deploy Script

```bash
# Interactive setup
./deploy.sh

# Or directly
./deploy.sh build    # Build only
./deploy.sh server   # Configure & run server
./deploy.sh client   # Configure & run client
./deploy.sh genkey   # Generate keys
```

---

## 🐳 Docker

### Build Images

```bash
# Server only
docker build --target server -t slipstream-server .

# Client only
docker build --target client -t slipstream-client .

# Combined
docker build -t slipstream-go .
```

### Run with Docker Compose

```bash
# Copy and edit environment
cp .env.example .env

# Start server
docker-compose up -d server

# Start client (different machine)
docker-compose up -d client
```

### Manual Docker Run

<details>
<summary>📋 Click to expand</summary>

**Server:**
```bash
docker run -d \
  --name slipstream-server \
  -p 5353:5353/udp \
  -v $(pwd)/keys:/app/keys:ro \
  slipstream-server \
  --domain tunnel.example.com \
  --privkey-file /app/keys/server.key
```

**Client:**
```bash
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

</details>

---

## 📖 Usage

### Server Options

| Flag | Default | Description |
|:-----|:--------|:------------|
| `--domain` | *required* | Allowed tunnel domain (repeatable) |
| `--dns-port` | `5353` | DNS server port |
| `--target-type` | `direct` | `direct` or `socks5` |
| `--target` | - | Upstream SOCKS5 address |
| `--privkey-file` | *required* | Ed25519 private key |
| `--log-level` | `info` | `debug`/`info`/`warn`/`error` |
| `--memory-limit` | `400` | Memory limit in MB |

### Client Options

| Flag | Default | Description |
|:-----|:--------|:------------|
| `--domain` | *required* | Tunnel domain |
| `--resolver` | `127.0.0.1:5353` | DNS resolver address |
| `--listen` | `127.0.0.1:1080` | Local SOCKS5 address |
| `--pubkey-file` | *required* | Server public key |
| `--log-level` | `info` | `debug`/`info`/`warn`/`error` |

### Multi-Domain Example

```bash
./bin/slipstream-server \
  --domain tunnel1.example.com \
  --domain tunnel2.example.com \
  --domain vpn.example.org \
  --privkey-file server.key
```

> ⚠️ Clients with unregistered domains receive DNS REFUSED

---

## 🔧 Architecture

```
┌─────────────────┐                              ┌─────────────────┐
│                 │         DNS Queries          │                 │
│   Slipstream    │  ─────────────────────────►  │   Slipstream    │
│   Client        │     (QUIC in Base32)         │   Server        │
│                 │                              │                 │
│  ┌───────────┐  │  ◄─────────────────────────  │  ┌───────────┐  │
│  │  SOCKS5   │  │      DNS TXT Responses       │  │    DNS    │  │
│  │  Proxy    │  │     (QUIC in Base64)         │  │  Handler  │  │
│  └───────────┘  │                              │  └───────────┘  │
└────────┬────────┘                              └────────┬────────┘
         │                                                │
         │ SOCKS5                                         │ Direct/SOCKS5
         ▼                                                ▼
   ┌──────────┐                                    ┌──────────────┐
   │   Apps   │                                    │   Internet   │
   └──────────┘                                    └──────────────┘
```

### Data Flow

1. **App** connects to local SOCKS5 proxy
2. **Client** encodes QUIC packets as Base32 in DNS labels
3. **DNS queries** traverse the network
4. **Server** decodes and forwards via QUIC
5. **Responses** return as Base64 in TXT records

---

## 📋 Logs

### Log Levels

| Level | Use Case |
|:------|:---------|
| `debug` | Development, packet inspection |
| `info` | Production, connection events |
| `warn` | Rejected connections, issues |
| `error` | Errors only |

### Sample Output

<details>
<summary>🖥️ Server Startup</summary>

```
6:00PM INF Registered allowed domain domain=tunnel.example.com
6:00PM INF Private key loaded
6:00PM INF QUIC listener started on virtual connection
6:00PM INF Starting DNS server addr=:5353 domains=1
```

</details>

<details>
<summary>💻 Client Connection</summary>

```
6:00PM INF Using server public key fingerprint=MA8SxzbX...
6:00PM INF Generated session ID session=abc12345
6:00PM INF Establishing QUIC connection over DNS
6:00PM INF QUIC tunnel established
6:00PM INF SOCKS5 server listening addr=127.0.0.1:1080
```

</details>

<details>
<summary>🔄 Auto-Reconnection</summary>

```
6:00PM WRN Connection health check failed, reconnecting...
6:00PM INF Attempting reconnection attempt=1
6:00PM INF QUIC tunnel re-established
```

</details>

---

## 🌐 DNS Configuration

For production, delegate a subdomain to your server:

```dns
; Zone file for example.com
tunnel    IN    NS    ns-tunnel.example.com.
ns-tunnel IN    A     YOUR_SERVER_IP
```

Then run on port 53:
```bash
./bin/slipstream-server --domain tunnel.example.com --dns-port 53 ...
```

---

## 🔒 Security

| Aspect | Implementation |
|:-------|:---------------|
| **Authentication** | Ed25519 key pairs |
| **Certificate Pinning** | Client validates server pubkey |
| **Domain Validation** | Server rejects unknown domains |
| **Memory Protection** | Configurable limits |

> 🔑 **Never share private keys** - only distribute `.pub` files to clients

---

## 🐛 Troubleshooting

<details>
<summary><b>Connection Timeout</b></summary>

- Verify DNS port is accessible (UDP)
- Check firewall rules
- Ensure domain is registered on server

</details>

<details>
<summary><b>Slow Performance</b></summary>

- Check network latency to DNS server
- Enable debug logging
- Verify no packet loss

</details>

<details>
<summary><b>Authentication Failure</b></summary>

- Verify public key matches server's private key
- Check key file permissions
- Regenerate keys if needed

</details>

---

## 🤝 Contributing

Contributions welcome! Feel free to:

- 🐛 Report bugs
- 💡 Suggest features
- 🔧 Submit pull requests

---

## 📄 License

MIT License - see [LICENSE](LICENSE) for details.

---

## 🙏 Acknowledgments

- Based on [slipstream-rust](https://github.com/Mygod/slipstream-rust/) by Mygod
- Original concept [slipstream](https://github.com/EndPositive/slipstream) by EndPositive
- Inspired by [iodine](https://code.kryo.se/iodine/), [dnscat2](https://github.com/iagox86/dnscat2)
- Built with [quic-go](https://github.com/quic-go/quic-go)
- DNS handling by [miekg/dns](https://github.com/miekg/dns)

---

<p align="center">
  <sub>Built with ❤️ for network freedom</sub>
</p>
