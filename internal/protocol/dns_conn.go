package protocol

import (
	"encoding/base32"
	"encoding/base64"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/miekg/dns"
	"github.com/rs/zerolog/log"
)

const (
	TxQueueSize  = 2000
	RxQueueSize  = 2000
	NumTxWorkers = 4
	PollInterval = 10 * time.Millisecond // Fast polling for low latency
	WriteTimeout = 5 * time.Second
)

type DnsPacketConn struct {
	Resolver  *net.UDPAddr
	Domain    string
	SessionID string
	Conn      *net.UDPConn

	rxQueue     chan []byte
	txQueue     chan []byte
	closeOnce   sync.Once
	done        chan struct{}
	lastTxTime  time.Time
	mu          sync.Mutex // Protects lastTxTime
	reassembler *Reassembler
}

func NewDnsPacketConn(resolver, domain, sessionID string) (*DnsPacketConn, error) {
	rAddr, err := net.ResolveUDPAddr("udp", resolver)
	if err != nil {
		return nil, err
	}

	conn, err := net.ListenUDP("udp", nil)
	if err != nil {
		return nil, err
	}
	// Increase OS buffer to avoid drops
	conn.SetReadBuffer(4 * 1024 * 1024)

	c := &DnsPacketConn{
		Resolver:    rAddr,
		Domain:      domain,
		SessionID:   sessionID,
		Conn:        conn,
		rxQueue:     make(chan []byte, RxQueueSize),
		txQueue:     make(chan []byte, TxQueueSize),
		done:        make(chan struct{}),
		reassembler: NewReassembler(),
	}

	c.startRxEngine()
	c.startTxEngine()
	c.startPollEngine()

	return c, nil
}

// SPOOFING: Lie to QUIC that we are UDP
func (c *DnsPacketConn) LocalAddr() net.Addr {
	return &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0}
}
func (c *DnsPacketConn) SetReadDeadline(t time.Time) error  { return nil }
func (c *DnsPacketConn) SetWriteDeadline(t time.Time) error { return nil }
func (c *DnsPacketConn) Close() error {
	c.closeOnce.Do(func() { close(c.done); c.Conn.Close() })
	return nil
}

// WRITE: Fragment & Queue (Backpressure enabled)
func (c *DnsPacketConn) WriteTo(p []byte, addr net.Addr) (n int, err error) {
	// IGNORE 'addr' (It is the dummy 127.0.0.1 from QUIC)

	// Update Activity
	c.mu.Lock()
	c.lastTxTime = time.Now()
	c.mu.Unlock()

	// 1. Fragment Logic
	fragments := FragmentPacket(p)

	// 2. Queue Fragments
	for _, frag := range fragments {
		select {
		case c.txQueue <- frag:
		case <-time.After(WriteTimeout):
			// Backpressure: If queue full, block then drop
			log.Warn().Msg("TX Queue Full - Drop")
			return 0, nil // Return nil so QUIC doesn't crash, just retransmits
		case <-c.done:
			return 0, net.ErrClosed
		}
	}
	return len(p), nil
}

// READ: Return from Queue (Spoofing Address)
func (c *DnsPacketConn) ReadFrom(p []byte) (n int, addr net.Addr, err error) {
	select {
	case data := <-c.rxQueue:
		n = copy(p, data)
		// Return our Fake UDP Addr so QUIC accepts it
		return n, c.LocalAddr(), nil
	case <-c.done:
		return 0, nil, net.ErrClosed
	}
}

// --- ENGINES ---

func (c *DnsPacketConn) startTxEngine() {
	for i := 0; i < NumTxWorkers; i++ {
		go func() {
			msg := new(dns.Msg)
			// Format: [DATA-LABELS].[SESSION].[DOMAIN]
			suffix := "." + c.SessionID + "." + c.Domain + "."

			for {
				select {
				case pkt := <-c.txQueue:
					// Use NoPadding base32 to avoid = characters in DNS labels
					encoded := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(pkt)

					// Split encoded data into 63-char labels (DNS label limit)
					dataLabels := splitIntoLabels(encoded, 63)
					qname := dataLabels + suffix

					msg.SetQuestion(qname, dns.TypeTXT)
					buf, _ := msg.Pack()
					c.Conn.WriteToUDP(buf, c.Resolver)
				case <-c.done:
					return
				}
			}
		}()
	}
}

// splitIntoLabels splits a string into DNS labels of max length
func splitIntoLabels(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	var result strings.Builder
	for i := 0; i < len(s); i += maxLen {
		if i > 0 {
			result.WriteByte('.')
		}
		end := i + maxLen
		if end > len(s) {
			end = len(s)
		}
		result.WriteString(s[i:end])
	}
	return result.String()
}

func (c *DnsPacketConn) startRxEngine() {
	go func() {
		buf := make([]byte, 4096)
		for {
			n, _, err := c.Conn.ReadFromUDP(buf)
			if err != nil {
				select {
				case <-c.done:
					return
				default:
					continue
				}
			}

			msg := new(dns.Msg)
			if err := msg.Unpack(buf[:n]); err != nil {
				log.Debug().Err(err).Msg("Failed to unpack DNS response")
				continue
			}

			gotData := false
			for _, ans := range msg.Answer {
				if txt, ok := ans.(*dns.TXT); ok {
					// Join TXT chunks (miekg/dns may split at 255 chars)
					encoded := strings.Join(txt.Txt, "")

					// Decode base64 fragment
					raw, err := base64.StdEncoding.DecodeString(encoded)
					if err != nil {
						log.Debug().Err(err).Int("len", len(encoded)).Msg("Failed to decode base64 TXT")
						continue
					}

					if len(raw) > 0 {
						gotData = true
						log.Debug().Int("rawLen", len(raw)).Msg("Received fragment from server")
						// Reassemble fragments into full packets
						if fullPacket := c.reassembler.IngestChunk(raw); fullPacket != nil {
							log.Debug().Int("pktLen", len(fullPacket)).Msg("Reassembled full packet")
							// Push complete packet to QUIC
							select {
							case c.rxQueue <- fullPacket:
							default:
								log.Warn().Msg("RX queue full, dropping packet")
							}
						}
					}
				}
			}

			// Turbo Poll: If we got any data, immediately ask for more
			// Send inline (not goroutine) for minimum latency
			if gotData {
				c.sendPoll()
			}
		}
	}()
}

func (c *DnsPacketConn) startPollEngine() {
	go func() {
		ticker := time.NewTicker(PollInterval)
		for {
			select {
			case <-ticker.C:
				// Only poll if idle for > 50ms
				c.mu.Lock()
				idle := time.Since(c.lastTxTime) > 50*time.Millisecond
				c.mu.Unlock()

				if idle {
					c.sendPoll()
				}
			case <-c.done:
				return
			}
		}
	}()
}

func (c *DnsPacketConn) sendPoll() {
	// "poll" is a magic keyword for the server
	// Format: poll.SESSION.DOMAIN. (no leading dot)
	qname := "poll." + c.SessionID + "." + c.Domain + "."
	msg := new(dns.Msg)
	msg.SetQuestion(qname, dns.TypeTXT)
	buf, _ := msg.Pack()
	c.Conn.WriteToUDP(buf, c.Resolver)
}

func (c *DnsPacketConn) SetDeadline(t time.Time) error {
	// Forward the call to the underlying UDP connection
	return c.Conn.SetDeadline(t)
}
