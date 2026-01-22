package main

import (
	"context"
	cryptorand "crypto/rand"
	"crypto/tls"
	"encoding/binary"
	"flag"
	"io"
	"net"
	"os"
	"runtime/debug"
	"sync"
	"sync/atomic"
	"time"

	"github.com/quic-go/quic-go"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"

	"slipstream-go/internal/crypto"
	"slipstream-go/internal/protocol"
	"slipstream-go/internal/proxy"
)

// TunnelManager manages the QUIC connection with auto-reconnection
type TunnelManager struct {
	resolver    string
	domain      string
	tlsConfig   *tls.Config
	quicConfig  *quic.Config

	conn      *quic.Conn
	dnsConn   *protocol.DnsPacketConn
	sessionID string
	mu        sync.RWMutex

	connected   atomic.Bool
	reconnecting atomic.Bool
}

// NewTunnelManager creates a new tunnel manager
func NewTunnelManager(resolver, domain string, tlsConfig *tls.Config) *TunnelManager {
	return &TunnelManager{
		resolver:  resolver,
		domain:    domain,
		tlsConfig: tlsConfig,
		quicConfig: &quic.Config{
			KeepAlivePeriod:            10 * time.Second,
			MaxIdleTimeout:             60 * time.Second,
			MaxStreamReceiveWindow:     6 * 1024 * 1024,
			MaxConnectionReceiveWindow: 15 * 1024 * 1024,
			// Optimal MTU for Iran: 512-768 bytes (benchmarked)
			// 600 bytes / 120 bytes per fragment = 5 fragments
			// QUIC Initial packets will still be padded to 1200 bytes per spec
			InitialPacketSize: 600,
			// Disable PMTU discovery to keep packets small after handshake
			DisablePathMTUDiscovery: true,
		},
	}
}

// Connect establishes the QUIC connection
func (tm *TunnelManager) Connect() error {
	tm.mu.Lock()
	defer tm.mu.Unlock()

	// Close existing connection if any
	if tm.dnsConn != nil {
		tm.dnsConn.Close()
	}

	// Generate new session ID for each connection
	tm.sessionID = generateSessionID()
	log.Info().Str("session", tm.sessionID).Msg("Generated session ID")

	// Setup DNS transport
	dnsConn, err := protocol.NewDnsPacketConn(tm.resolver, tm.domain, tm.sessionID)
	if err != nil {
		return err
	}
	tm.dnsConn = dnsConn

	// Dummy address for QUIC
	dummyAddr := &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 1234}

	// Establish QUIC connection
	log.Info().Str("resolver", tm.resolver).Str("domain", tm.domain).Msg("Establishing QUIC connection over DNS")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	quicConn, err := quic.Dial(ctx, dnsConn, dummyAddr, tm.tlsConfig, tm.quicConfig)
	if err != nil {
		dnsConn.Close()
		return err
	}

	tm.conn = quicConn
	tm.connected.Store(true)
	log.Info().Msg("QUIC tunnel established")

	return nil
}

// GetConnection returns the current QUIC connection
func (tm *TunnelManager) GetConnection() *quic.Conn {
	tm.mu.RLock()
	defer tm.mu.RUnlock()
	return tm.conn
}

// IsConnected returns whether the tunnel is connected
func (tm *TunnelManager) IsConnected() bool {
	return tm.connected.Load()
}

// MarkDisconnected marks the tunnel as disconnected
func (tm *TunnelManager) MarkDisconnected() {
	tm.connected.Store(false)
}

// Reconnect attempts to reconnect with exponential backoff
func (tm *TunnelManager) Reconnect() {
	// Prevent multiple reconnection attempts
	if tm.reconnecting.Load() {
		return
	}
	tm.reconnecting.Store(true)
	defer tm.reconnecting.Store(false)

	tm.MarkDisconnected()

	backoff := 1 * time.Second
	maxBackoff := 30 * time.Second

	for {
		log.Warn().Dur("backoff", backoff).Msg("Attempting to reconnect...")

		err := tm.Connect()
		if err == nil {
			log.Info().Msg("Reconnected successfully")
			return
		}

		log.Error().Err(err).Msg("Reconnection failed")

		time.Sleep(backoff)
		backoff *= 2
		if backoff > maxBackoff {
			backoff = maxBackoff
		}
	}
}

// StartHealthCheck monitors connection health and triggers reconnection
func (tm *TunnelManager) StartHealthCheck() {
	go func() {
		for {
			time.Sleep(5 * time.Second)

			conn := tm.GetConnection()
			if conn == nil {
				continue
			}

			// Check if connection is still alive by checking context
			select {
			case <-conn.Context().Done():
				log.Warn().Msg("Connection lost, initiating reconnection")
				go tm.Reconnect()
			default:
				// Connection is still alive
			}
		}
	}()
}

func main() {
	// CLI Flags
	domain := flag.String("domain", "", "Tunnel domain (required)")
	listen := flag.String("listen", "127.0.0.1:1080", "Local SOCKS5 listen address")
	resolver := flag.String("resolver", "", "DNS resolver address (server) (required)")
	pubkeyFile := flag.String("pubkey-file", "", "Server public key for pinning (required)")
	logLevel := flag.String("log-level", "info", "Log level: debug/info/warn/error")
	memoryLimit := flag.Int("memory-limit", 200, "Memory limit in MB")

	flag.Parse()

	// Setup logging
	zerolog.TimeFieldFormat = zerolog.TimeFormatUnix
	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr})

	switch *logLevel {
	case "debug":
		zerolog.SetGlobalLevel(zerolog.DebugLevel)
	case "info":
		zerolog.SetGlobalLevel(zerolog.InfoLevel)
	case "warn":
		zerolog.SetGlobalLevel(zerolog.WarnLevel)
	case "error":
		zerolog.SetGlobalLevel(zerolog.ErrorLevel)
	default:
		log.Fatal().Str("level", *logLevel).Msg("Invalid log level")
	}

	// Set memory limit
	debug.SetMemoryLimit(int64(*memoryLimit) * 1024 * 1024)

	// Validate required flags
	if *domain == "" {
		log.Fatal().Msg("--domain is required")
	}
	if *resolver == "" {
		log.Fatal().Msg("--resolver is required")
	}
	if *pubkeyFile == "" {
		log.Fatal().Msg("--pubkey-file is required")
	}

	// Load public key and calculate fingerprint
	pubKey, err := crypto.LoadPublicKey(*pubkeyFile)
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to load public key")
	}
	fingerprint := crypto.PublicKeyFingerprint(pubKey)
	log.Info().Str("fingerprint", fingerprint).Msg("Using server public key")

	// Create TLS config with certificate pinning
	tlsConfig := crypto.GetClientTLSConfig(fingerprint)

	// Create tunnel manager
	tunnel := NewTunnelManager(*resolver, *domain, tlsConfig)

	// Initial connection
	if err := tunnel.Connect(); err != nil {
		log.Fatal().Err(err).Msg("Initial connection failed")
	}

	// Start health check for auto-reconnection
	tunnel.StartHealthCheck()

	// Start local SOCKS5 server
	listener, err := net.Listen("tcp", *listen)
	if err != nil {
		log.Fatal().Err(err).Str("addr", *listen).Msg("Failed to start SOCKS5 listener")
	}
	log.Info().Str("addr", *listen).Msg("SOCKS5 server listening")

	for {
		conn, err := listener.Accept()
		if err != nil {
			log.Error().Err(err).Msg("Failed to accept connection")
			continue
		}

		go handleSOCKS5Connection(conn, tunnel)
	}
}

// generateSessionID creates a random session ID using crypto/rand
func generateSessionID() string {
	const charset = "abcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, 8)
	cryptorand.Read(b)
	for i := range b {
		b[i] = charset[int(b[i])%len(charset)]
	}
	return string(b)
}

// handleSOCKS5Connection handles an incoming SOCKS5 connection from a local app
func handleSOCKS5Connection(conn net.Conn, tunnel *TunnelManager) {
	defer conn.Close()

	// Check if tunnel is connected
	if !tunnel.IsConnected() {
		log.Warn().Msg("Tunnel not connected, rejecting SOCKS5 request")
		sendSOCKS5Error(conn, 0x01)
		return
	}

	// SOCKS5 greeting
	buf := make([]byte, 258)

	// Read greeting: version + nmethods + methods
	if _, err := io.ReadFull(conn, buf[:2]); err != nil {
		log.Debug().Err(err).Msg("Failed to read SOCKS5 greeting")
		return
	}

	if buf[0] != 0x05 {
		log.Debug().Uint8("version", buf[0]).Msg("Not SOCKS5")
		return
	}

	nmethods := int(buf[1])
	if _, err := io.ReadFull(conn, buf[:nmethods]); err != nil {
		log.Debug().Err(err).Msg("Failed to read SOCKS5 methods")
		return
	}

	// Reply: no authentication required
	conn.Write([]byte{0x05, 0x00})

	// Read CONNECT request: version, cmd, reserved, atype, addr, port
	if _, err := io.ReadFull(conn, buf[:4]); err != nil {
		log.Debug().Err(err).Msg("Failed to read SOCKS5 request")
		return
	}

	if buf[0] != 0x05 || buf[1] != 0x01 {
		log.Debug().Msg("Not a CONNECT request")
		sendSOCKS5Error(conn, 0x07) // Command not supported
		return
	}

	// Parse address
	addrType := buf[3]
	var targetAddr string
	var port uint16

	switch addrType {
	case 0x01: // IPv4
		if _, err := io.ReadFull(conn, buf[:4]); err != nil {
			return
		}
		targetAddr = net.IP(buf[:4]).String()

	case 0x03: // Domain
		if _, err := io.ReadFull(conn, buf[:1]); err != nil {
			return
		}
		domainLen := int(buf[0])
		if _, err := io.ReadFull(conn, buf[:domainLen]); err != nil {
			return
		}
		targetAddr = string(buf[:domainLen])

	case 0x04: // IPv6
		if _, err := io.ReadFull(conn, buf[:16]); err != nil {
			return
		}
		targetAddr = net.IP(buf[:16]).String()

	default:
		sendSOCKS5Error(conn, 0x08) // Address type not supported
		return
	}

	// Read port
	if _, err := io.ReadFull(conn, buf[:2]); err != nil {
		return
	}
	port = binary.BigEndian.Uint16(buf[:2])

	fullAddr := net.JoinHostPort(targetAddr, portToString(port))

	log.Debug().Str("target", fullAddr).Msg("SOCKS5 CONNECT request")

	// Get current QUIC connection
	quicConn := tunnel.GetConnection()
	if quicConn == nil {
		log.Error().Msg("No QUIC connection available")
		sendSOCKS5Error(conn, 0x01)
		return
	}

	// Open QUIC stream with timeout
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	stream, err := quicConn.OpenStreamSync(ctx)
	if err != nil {
		log.Error().Err(err).Msg("Failed to open QUIC stream")
		sendSOCKS5Error(conn, 0x01)

		// Trigger reconnection if stream opening fails
		go tunnel.Reconnect()
		return
	}
	defer stream.Close()

	// Send target address to server via stream header
	if err := proxy.WriteTargetAddress(stream, fullAddr); err != nil {
		log.Error().Err(err).Msg("Failed to write target address")
		sendSOCKS5Error(conn, 0x01)
		return
	}

	// Read server response (1 byte: 0x00 = success, 0x01 = error)
	respBuf := make([]byte, 1)
	if _, err := io.ReadFull(stream, respBuf); err != nil {
		log.Error().Err(err).Msg("Failed to read server response")
		sendSOCKS5Error(conn, 0x01)
		return
	}

	if respBuf[0] != 0x00 {
		log.Debug().Msg("Server reported connection failure")
		sendSOCKS5Error(conn, 0x05) // Connection refused
		return
	}

	// Send SOCKS5 success response
	response := []byte{
		0x05, 0x00, 0x00, 0x01,
		0x00, 0x00, 0x00, 0x00, // Bind address (0.0.0.0)
		0x00, 0x00, // Bind port (0)
	}
	conn.Write(response)

	log.Debug().Str("target", fullAddr).Msg("SOCKS5 tunnel established")

	// Bidirectional pipe
	done := make(chan struct{}, 2)

	go func() {
		io.Copy(stream, conn)
		done <- struct{}{}
	}()

	go func() {
		io.Copy(conn, stream)
		done <- struct{}{}
	}()

	<-done
}

func sendSOCKS5Error(conn net.Conn, code byte) {
	response := []byte{
		0x05, code, 0x00, 0x01,
		0x00, 0x00, 0x00, 0x00,
		0x00, 0x00,
	}
	conn.Write(response)
}

func portToString(port uint16) string {
	result := make([]byte, 0, 5)
	if port == 0 {
		return "0"
	}
	for port > 0 {
		result = append([]byte{byte('0' + port%10)}, result...)
		port /= 10
	}
	return string(result)
}
