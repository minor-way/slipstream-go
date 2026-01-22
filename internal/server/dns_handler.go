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
	// Example: AAAA.BBBB.sess123.tunnel.local.
	// Data may span multiple labels (each up to 63 chars)
	qName := r.Question[0].Name
	labels := dns.SplitDomainName(qName)
	if len(labels) < 3 {
		return
	}

	// Find session ID - it's followed by domain parts
	// Domain is typically 2 parts (e.g., "tunnel.local"), so session is at len-2
	// But we need to handle variable domain lengths. Session ID is always after data labels.
	// Convention: session ID is alphanumeric 8 chars, followed by domain
	// For simplicity, we assume: all labels before the last 2 (domain) except data are session
	// Actually, let's use a simpler approach: session is second-to-last before domain
	// labels[-3] = session, labels[-2] = tunnel, labels[-1] = local

	// Extract session ID (assuming domain has 2 parts like "tunnel.local")
	if len(labels) < 4 {
		// Minimum: data.session.tunnel.local = 4 labels
		return
	}

	// Extract and validate domain (last 2 labels, e.g., "tunnel.local")
	domain := strings.ToLower(labels[len(labels)-2] + "." + labels[len(labels)-1])
	if h.AllowedDomains != nil && !h.AllowedDomains[domain] {
		log.Warn().Str("domain", domain).Str("query", qName).Msg("Rejected query for unregistered domain")
		// Send REFUSED response
		msg := new(dns.Msg)
		msg.SetRcode(r, dns.RcodeRefused)
		w.WriteMsg(msg)
		return
	}

	// Session is at index len-3 (third from last)
	sessionID := labels[len(labels)-3]

	// Data labels are everything before session
	dataLabels := labels[:len(labels)-3]
	dataLabel := strings.Join(dataLabels, "")

	sess := h.Sessions.GetOrCreate(sessionID)

	// 1. INGEST UPSTREAM (Reassembly)
	// If it's not a "poll" query, it contains data chunks
	if !strings.HasPrefix(dataLabel, "poll") {
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
	// Each base64-encoded fragment is ~165 bytes
	maxFrags := h.MaxFragsPerResponse
	if maxFrags <= 0 {
		maxFrags = 5 // default
	}
	fragsSent := 0

	// Send fragments from queue until limit reached
	for fragsSent < maxFrags {
		select {
		case frag := <-sess.FragQueue:
			encoded := base64.StdEncoding.EncodeToString(frag)
			msg.Answer = append(msg.Answer, &dns.TXT{
				Hdr: dns.RR_Header{Name: qName, Rrtype: dns.TypeTXT, Class: dns.ClassINET, Ttl: 0},
				Txt: []string{encoded},
			})
			fragsSent++
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
