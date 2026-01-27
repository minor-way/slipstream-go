package main

import (
	"context"
	cryptorand "crypto/rand"
	"encoding/binary"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"runtime/debug"
	"strings"
	"time"

	"github.com/miekg/dns"
	"github.com/quic-go/quic-go"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"

	"slipstream-go/internal/crypto"
	"slipstream-go/internal/proxy"
	"slipstream-go/internal/server"
)

// randomPacketSize returns a random packet size between 512 and 768 bytes
// This range is optimal for Iran's DNS resolvers (benchmarked)
func randomPacketSize() uint16 {
	b := make([]byte, 2)
	cryptorand.Read(b)
	// Range: 512 + (random % 257) = 512 to 768
	return 512 + (binary.BigEndian.Uint16(b) % 257)
}

// stringSlice is a custom flag type for multiple string values
type stringSlice []string

func (s *stringSlice) String() string {
	return strings.Join(*s, ", ")
}

func (s *stringSlice) Set(value string) error {
	*s = append(*s, value)
	return nil
}

func main() {
	// CLI Flags
	var domains stringSlice
	flag.Var(&domains, "domain", "Allowed tunnel domain (can be specified multiple times)")
	dnsPort := flag.Int("dns-port", 5353, "DNS server port")
	targetType := flag.String("target-type", "direct", "Target type: direct or socks5")
	target := flag.String("target", "", "Upstream SOCKS5 address (required if target-type=socks5)")
	privkeyFile := flag.String("privkey-file", "", "Ed25519 private key file")
	pubkeyFile := flag.String("pubkey-file", "", "Public key output file (with --gen-key)")
	genKey := flag.Bool("gen-key", false, "Generate keys and exit")
	logLevel := flag.String("log-level", "info", "Log level: debug/info/warn/error")
	memoryLimit := flag.Int("memory-limit", 400, "Memory limit in MB")
	maxFrags := flag.Int("max-frags", 6, "Max fragments per DNS response (1-20, default 6 with EDNS0)")

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

	// Handle key generation
	if *genKey {
		if *privkeyFile == "" {
			log.Fatal().Msg("--privkey-file required with --gen-key")
		}
		if *pubkeyFile == "" {
			log.Fatal().Msg("--pubkey-file required with --gen-key")
		}

		pubKey, privKey, err := crypto.GenerateKeyPair()
		if err != nil {
			log.Fatal().Err(err).Msg("Failed to generate key pair")
		}

		if err := crypto.SavePrivateKey(privKey, *privkeyFile); err != nil {
			log.Fatal().Err(err).Msg("Failed to save private key")
		}
		log.Info().Str("path", *privkeyFile).Msg("Private key saved")

		if err := crypto.SavePublicKey(pubKey, *pubkeyFile); err != nil {
			log.Fatal().Err(err).Msg("Failed to save public key")
		}
		log.Info().Str("path", *pubkeyFile).Msg("Public key saved")

		fingerprint := crypto.PublicKeyFingerprint(pubKey)
		log.Info().Str("fingerprint", fingerprint).Msg("Public key fingerprint")

		os.Exit(0)
	}

	// Validate required flags
	if len(domains) == 0 {
		log.Fatal().Msg("At least one --domain is required")
	}
	if *privkeyFile == "" {
		log.Fatal().Msg("--privkey-file is required")
	}
	if *targetType == "socks5" && *target == "" {
		log.Fatal().Msg("--target is required when --target-type=socks5")
	}

	// Build allowed domains set (normalize to lowercase)
	allowedDomains := make(map[string]bool)
	for _, d := range domains {
		normalized := strings.ToLower(strings.TrimSuffix(d, "."))
		allowedDomains[normalized] = true
		log.Info().Str("domain", normalized).Msg("Registered allowed domain")
	}

	// Load private key
	privKey, err := crypto.LoadPrivateKey(*privkeyFile)
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to load private key")
	}
	log.Info().Msg("Private key loaded")

	// Create TLS config
	tlsConfig, err := crypto.GetTLSConfig(privKey)
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to create TLS config")
	}

	// Create session manager
	sessionMgr := server.NewSessionManager()

	// Create virtual connection (bridges DNS <-> QUIC)
	virtualConn := server.NewVirtualConn(sessionMgr)

	// Create DNS handler with allowed domains
	dnsHandler := &server.DNSHandler{
		Sessions:            sessionMgr,
		Injector:            virtualConn,
		AllowedDomains:      allowedDomains,
		MaxFragsPerResponse: *maxFrags,
	}

	// Start DNS server
	dnsAddr := fmt.Sprintf(":%d", *dnsPort)
	dnsServer := &dns.Server{
		Addr:    dnsAddr,
		Net:     "udp",
		Handler: dns.HandlerFunc(dnsHandler.HandleDNS),
	}

	go func() {
		log.Info().Str("addr", dnsAddr).Int("domains", len(allowedDomains)).Msg("Starting DNS server")
		if err := dnsServer.ListenAndServe(); err != nil {
			log.Fatal().Err(err).Msg("DNS server failed")
		}
	}()

	// Create Transport with address validation to force Retry packets
	// This bypasses the 3x amplification limit that causes handshake deadlock
	// when certificate chain exceeds 3600 bytes and ACKs get lost in DNS tunnel
	transport := &quic.Transport{
		Conn: virtualConn,
		// CRITICAL: Force address validation via Retry packet for ALL connections
		VerifySourceAddress: func(net.Addr) bool { return true },
	}

	// Create QUIC listener on transport
	packetSize := randomPacketSize()
	log.Info().Uint16("packet_size", packetSize).Msg("Using random packet size")
	quicListener, err := transport.Listen(tlsConfig, &quic.Config{
		KeepAlivePeriod:            35 * time.Second, // Send keepalive every 35s
		MaxIdleTimeout:             5 * time.Minute,  // 5 minute idle timeout
		EnableDatagrams:            false,
		MaxIncomingStreams:         1000,
		MaxIncomingUniStreams:      1000,
		MaxStreamReceiveWindow:     6 * 1024 * 1024,
		MaxConnectionReceiveWindow: 15 * 1024 * 1024,
		// Random packet size in optimal range for Iran: 512-768 bytes
		InitialPacketSize:       packetSize,
		DisablePathMTUDiscovery: true,
	})
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to create QUIC listener")
	}
	log.Info().Msg("QUIC listener started on virtual connection")

	// Setup dialer based on target type
	var dialer Dialer
	if *targetType == "socks5" {
		dialer = &socks5Dialer{proxy: proxy.NewSOCKS5Dialer(*target)}
		log.Info().Str("proxy", *target).Msg("Using SOCKS5 upstream")
	} else {
		dialer = &directDialer{}
		log.Info().Msg("Using direct connections")
	}

	// Accept QUIC connections
	for {
		conn, err := quicListener.Accept(context.Background())
		if err != nil {
			log.Error().Err(err).Msg("Failed to accept QUIC connection")
			continue
		}

		log.Info().Str("remote", conn.RemoteAddr().String()).Msg("New QUIC connection")
		go handleQUICConnection(conn, dialer)
	}
}

// Dialer interface for connection abstraction
type Dialer interface {
	Dial(network, addr string) (net.Conn, error)
}

type directDialer struct{}

func (d *directDialer) Dial(network, addr string) (net.Conn, error) {
	return net.Dial(network, addr)
}

type socks5Dialer struct {
	proxy *proxy.SOCKS5Dialer
}

func (d *socks5Dialer) Dial(network, addr string) (net.Conn, error) {
	return d.proxy.Dial(network, addr)
}

func handleQUICConnection(conn *quic.Conn, dialer Dialer) {
	defer conn.CloseWithError(0, "")

	for {
		stream, err := conn.AcceptStream(context.Background())
		if err != nil {
			if !strings.Contains(err.Error(), "timeout") && !strings.Contains(err.Error(), "closed") {
				log.Error().Err(err).Msg("Failed to accept stream")
			}
			return
		}

		go handleStream(stream, dialer)
	}
}

func handleStream(stream *quic.Stream, dialer Dialer) {
	defer stream.Close()

	// Read target address from stream header
	targetAddr, err := proxy.ParseTargetAddress(stream)
	if err != nil {
		log.Error().Err(err).Msg("Failed to parse target address")
		stream.Write([]byte{0x01}) // Error response
		return
	}

	log.Debug().Str("target", targetAddr).Msg("Connecting to target")

	// Connect to target
	targetConn, err := dialer.Dial("tcp", targetAddr)
	if err != nil {
		log.Error().Err(err).Str("target", targetAddr).Msg("Failed to connect to target")
		stream.Write([]byte{0x01}) // Error response
		return
	}
	defer targetConn.Close()

	// Send success response
	if _, err := stream.Write([]byte{0x00}); err != nil {
		log.Error().Err(err).Msg("Failed to send success response")
		return
	}

	log.Debug().Str("target", targetAddr).Msg("Connected to target, piping data")

	// Bidirectional pipe
	done := make(chan struct{}, 2)

	go func() {
		io.Copy(targetConn, stream)
		done <- struct{}{}
	}()

	go func() {
		io.Copy(stream, targetConn)
		done <- struct{}{}
	}()

	// Wait for one direction to finish
	<-done
}
