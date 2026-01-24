package protocol

import (
	"encoding/base32"
	"encoding/base64"
	"encoding/binary"
	"math/rand"
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
	NumTxWorkers = 32
	// PollInterval: 25ms heartbeat for idle polling
	PollInterval = 25 * time.Millisecond
	WriteTimeout = 5 * time.Second
	// IdleThreshold: Only poll when truly idle (no recent TX activity)
	IdleThreshold = 100 * time.Millisecond
	// ParallelPolls: 16 for reliable handshake + BurstEngine for throughput
	// With max-frags=3, each poll fetches ~450 bytes. 16 polls = ~7KB per RTT.
	ParallelPolls = 16
)

type DnsPacketConn struct {
	Resolver  *net.UDPAddr
	Domain    string
	SessionID string
	Conn      *net.UDPConn

	rxQueue     chan []byte
	txQueue     chan []byte
	pollTrigger chan struct{} // Async trigger for burst polling
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
		pollTrigger: make(chan struct{}, 1), // Buffer 1 for auto-debouncing
		done:        make(chan struct{}),
		reassembler: NewReassembler(),
	}

	c.startRxEngine()
	c.startTxEngine()
	c.startPollEngine()
	c.startBurstEngine() // Async polling engine

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

	c.mu.Lock()
	c.lastTxTime = time.Now()
	c.mu.Unlock()

	fragments := FragmentPacket(p)

	// Redundancy strategy:
	// Handshake packets (Large) need redundancy but MUST BE PACED to avoid resolver drops.
	redundancy := 1
	if len(p) >= 1000 {
		redundancy = 2
	}

	for r := 0; r < redundancy; r++ {
		for _, frag := range fragments {
			select {
			case c.txQueue <- frag:
				// PACING FIX: Slight delay between queueing fragments
				// This prevents the txWorkers from blasting the resolver instantly
				if redundancy > 1 {
					time.Sleep(2 * time.Millisecond)
				}
			case <-time.After(WriteTimeout):
				log.Warn().Msg("TX Queue Full - Drop")
				return 0, nil
			case <-c.done:
				return 0, net.ErrClosed
			}
		}
		// Wait longer between redundancy batches
		if r < redundancy-1 {
			time.Sleep(10 * time.Millisecond)
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

					// Split encoded data into 57-char labels (matches Rust implementation)
					// Using 57 instead of 63 provides safety margin and matches picoquic
					dataLabels := splitIntoLabels(encoded, 57)
					qname := dataLabels + suffix

					msg.SetQuestion(qname, dns.TypeTXT)

					// EDNS0: Signal support for large UDP packets (1232 bytes)
					// Clear Extra first (msg is reused), then add OPT
					msg.Extra = nil
					opt := &dns.OPT{
						Hdr: dns.RR_Header{Name: ".", Rrtype: dns.TypeOPT},
					}
					opt.SetUDPSize(1232)
					msg.Extra = append(msg.Extra, opt)

					buf, _ := msg.Pack()

					// Send once - QUIC's built-in retransmission handles reliability
					// Double-sending was causing 2x overhead and congestion
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
						// Reassemble fragments into full packets (no per-fragment logging)
						if fullPacket := c.reassembler.IngestChunk(raw); fullPacket != nil {
							log.Info().Int("len", len(fullPacket)).Msg("Downstream packet complete")
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

			// Turbo Poll: If we got data, trigger async burst polling
			// Non-blocking: if BurstEngine is busy, signal is debounced
			if gotData {
				select {
				case c.pollTrigger <- struct{}{}:
				default:
					// Already triggered, no need to stack
				}
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
				// Only poll if idle (no recent TX activity)
				c.mu.Lock()
				idle := time.Since(c.lastTxTime) > IdleThreshold
				c.mu.Unlock()

				if idle {
					c.sendParallelPolls()
				}
			case <-c.done:
				return
			}
		}
	}()
}

// startBurstEngine handles async burst polling without blocking RxEngine
// This reduces effective RTT by not adding dead time to the receive loop
func (c *DnsPacketConn) startBurstEngine() {
	go func() {
		for {
			select {
			case <-c.pollTrigger:
				// Data received, blast parallel polls to keep pipe saturated
				c.sendParallelPolls()
			case <-c.done:
				return
			}
		}
	}()
}

// sendParallelPolls sends multiple polls simultaneously to maximize throughput
// Each poll has a unique nonce so resolver treats them as separate queries
func (c *DnsPacketConn) sendParallelPolls() {
	for i := 0; i < ParallelPolls; i++ {
		c.sendPoll()
		// Minimal pacing: 1ms every 8 polls to avoid UDP buffer overflow
		// 32 polls complete in ~4ms instead of blocking RxEngine
		if i > 0 && i%8 == 0 {
			time.Sleep(1 * time.Millisecond)
		}
	}
}

func (c *DnsPacketConn) sendPoll() {
	// "poll" is a magic keyword for the server
	// Format: poll.NONCE.SESSION.DOMAIN. (nonce busts DNS cache)
	// The random nonce ensures each poll is unique, preventing ISP/resolver
	// from returning cached responses (which caused 18x duplication)
	nonce := make([]byte, 4)
	binary.BigEndian.PutUint32(nonce, rand.Uint32())
	nonceStr := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(nonce)

	qname := "poll." + nonceStr + "." + c.SessionID + "." + c.Domain + "."
	msg := new(dns.Msg)
	msg.SetQuestion(qname, dns.TypeTXT)

	// EDNS0: Signal support for large UDP packets (1232 bytes)
	// This tells the resolver "Don't truncate! I can handle big responses!"
	opt := &dns.OPT{
		Hdr: dns.RR_Header{Name: ".", Rrtype: dns.TypeOPT},
	}
	opt.SetUDPSize(1232)
	msg.Extra = append(msg.Extra, opt)

	buf, _ := msg.Pack()
	c.Conn.WriteToUDP(buf, c.Resolver)
}

func (c *DnsPacketConn) SetDeadline(t time.Time) error {
	// Forward the call to the underlying UDP connection
	return c.Conn.SetDeadline(t)
}
