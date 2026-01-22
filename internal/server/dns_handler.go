package server

import (
	"encoding/base32"
	"encoding/base64"
	"encoding/binary"
	"strings"

	"github.com/miekg/dns"
	"github.com/rs/zerolog/log"
)

type DNSHandler struct {
	Sessions *SessionManager
	// Injector allows us to push reassembled UDP packets into the QUIC listener
	Injector *VirtualConn
	// AllowedDomains contains the list of registered tunnel domains
	AllowedDomains map[string]bool
	// MaxFragsPerResponse is the max number of fragments to pack per DNS response
	MaxFragsPerResponse int
}

func (h *DNSHandler) HandleDNS(w dns.ResponseWriter, r *dns.Msg) {
	if len(r.Question) == 0 {
		return
	}

	// Format: [DATA-LABELS...].[SESSION].[DOMAIN]
	// Example: AAAA.BBBB.sess123.n.godevgo.ir.
	// Data may span multiple labels (each up to 63 chars)
	// Domain can have variable number of parts (e.g., "n.godevgo.ir" = 3 parts)
	qName := r.Question[0].Name
	labels := dns.SplitDomainName(qName)
	if len(labels) < 3 {
		return
	}

	// Find matching domain by checking suffix against allowed domains
	// Domain can have 2+ parts (e.g., "tunnel.local" or "n.godevgo.ir")
	var matchedDomain string
	var domainLabelCount int

	qNameLower := strings.ToLower(qName)
	for domain := range h.AllowedDomains {
		domainWithDot := strings.ToLower(domain) + "."
		if strings.HasSuffix(qNameLower, "."+domainWithDot) || qNameLower == domainWithDot {
			matchedDomain = domain
			domainLabelCount = len(dns.SplitDomainName(domain))
			break
		}
	}

	if matchedDomain == "" {
		// Extract domain for logging (try last 2-3 labels)
		var domainForLog string
		if len(labels) >= 2 {
			domainForLog = strings.ToLower(labels[len(labels)-2] + "." + labels[len(labels)-1])
		}
		log.Warn().Str("domain", domainForLog).Str("query", qName).Msg("Rejected query for unregistered domain")
		// Send REFUSED response
		msg := new(dns.Msg)
		msg.SetRcode(r, dns.RcodeRefused)
		w.WriteMsg(msg)
		return
	}

	// Minimum labels: data + session + domain parts
	minLabels := 2 + domainLabelCount
	if len(labels) < minLabels {
		return
	}

	// Session is right before domain (at index len - domainLabelCount - 1)
	// Normalize to lowercase since DNS is case-insensitive
	sessionIdx := len(labels) - domainLabelCount - 1
	sessionID := strings.ToLower(labels[sessionIdx])

	// Data labels are everything before session
	dataLabels := labels[:sessionIdx]
	dataLabel := strings.Join(dataLabels, "")

	sess := h.Sessions.GetOrCreate(sessionID)

	// 1. INGEST UPSTREAM (Reassembly)
	// If it's not a "poll" query, it contains data chunks
	// Note: dataLabel is case-preserved for base32, but poll check should be case-insensitive
	if !strings.HasPrefix(strings.ToLower(dataLabel), "poll") {
		// DNS labels are often lowercased by resolvers.
		// Standard Base32 requires Uppercase. Fix it here:
		normalizedData := strings.ToUpper(dataLabel)

		// Use NoPadding base32 to match client encoding (avoids = in DNS labels)
		raw, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(normalizedData)
		if err == nil {
			// Debug: show fragment header info
			if len(raw) >= 4 {
				pktID := binary.BigEndian.Uint16(raw[0:2])
				total := raw[2]
				seq := raw[3]
				log.Debug().Int("rawLen", len(raw)).Str("sess", sessionID).
					Uint16("pktID", pktID).Uint8("total", total).Uint8("seq", seq).
					Msg("Received chunk")
			} else {
				log.Debug().Int("rawLen", len(raw)).Str("sess", sessionID).Msg("Received chunk (short)")
			}
			// Pass chunk to reassembler
			if fullPacket := sess.Reassembler.IngestChunk(raw); fullPacket != nil {
				// Inject packet into QUIC Listener
				if h.Injector != nil {
					h.Injector.InjectPacket(fullPacket, sessionID)
					log.Debug().Int("len", len(fullPacket)).Str("sess", sessionID).Msg("Upstream Packet Injected")
				}
			}
		} else {
			log.Debug().Err(err).Str("data", dataLabel).Int("len", len(dataLabel)).Msg("Base32 decode failed")
		}
	} else {
		log.Debug().Str("sess", sessionID).Msg("Poll query received")
	}

	// 2. SEND DOWNSTREAM (Fragment packing with size limit)
	msg := new(dns.Msg)
	msg.SetReply(r)
	msg.Compress = true

	// Pack multiple fragments per response (configurable via --max-frags)
	// Each base64-encoded fragment is ~180 bytes (132 raw * 4/3 base64 + header)
	// Packing more fragments reduces round-trips dramatically
	maxFrags := h.MaxFragsPerResponse
	if maxFrags <= 0 {
		maxFrags = 10 // default increased from 5 for better throughput
	}
	fragsSent := 0

	// Send fragments from queue until limit reached
	queueLen := len(sess.FragQueue)
	if queueLen > 0 {
		log.Debug().Str("sess", sessionID).Int("queueLen", queueLen).Msg("FragQueue has pending data")
	}
	for fragsSent < maxFrags {
		select {
		case frag := <-sess.FragQueue:
			encoded := base64.StdEncoding.EncodeToString(frag)
			msg.Answer = append(msg.Answer, &dns.TXT{
				Hdr: dns.RR_Header{Name: qName, Rrtype: dns.TypeTXT, Class: dns.ClassINET, Ttl: 0},
				Txt: []string{encoded},
			})
			fragsSent++
			log.Debug().Str("sess", sessionID).Int("fragLen", len(frag)).Int("fragsSent", fragsSent).Msg("Queued fragment for response")
		default:
			// Queue is empty
			goto sendResponse
		}
	}

sendResponse:
	if len(msg.Answer) > 0 {
		log.Debug().Int("count", len(msg.Answer)).Msg("Sending DNS Response")
	}
	w.WriteMsg(msg)
}
