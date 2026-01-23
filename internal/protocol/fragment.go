package protocol

import (
	"encoding/binary"
	"math/rand"
	"sync"
	"time"
)

// Header: [PacketID:2][TotalChunks:1][SeqNum:1] = 4 Bytes
const FragHeaderLen = 4

// Max payload per DNS query to stay safe (253 chars QNAME limit)
// Calculation based on Rust reference implementation:
//   - DNS QNAME max length: 253 chars
//   - Domain suffix (e.g., ".n.example.com."): ~20 chars typical
//   - Session ID (e.g., ".abcd1234."): ~10 chars
//   - Available for data labels: ~223 chars
//   - With dots every 57 chars (DNS label limit 63, minus safety): ~4 dots = 219 chars base32
//   - 219 base32 chars = 219 * 5 / 8 = 136 bytes raw
//   - Subtract 4 byte header = 132 bytes max payload
//
// For shorter domains, we can fit more data:
//   - Rust formula: mtu = (240 - domain_len) / 1.6
//   - For 20-char domain: ~137 bytes
//
// Use 124 bytes as default (provides extra safety margin for restrictive resolvers)
const MaxChunkSize = 124

func init() {
	rand.Seed(time.Now().UnixNano())
}

// Reassembler reassembles fragmented packets
type Reassembler struct {
	pending   map[uint16]*pendingPacket
	completed map[uint16]time.Time // Track recently completed packet IDs to ignore duplicates
	mu        sync.Mutex
}

type pendingPacket struct {
	Chunks    [][]byte
	Total     int
	Received  int
	CreatedAt time.Time
}

// NewReassembler creates a new Reassembler
func NewReassembler() *Reassembler {
	return &Reassembler{
		pending:   make(map[uint16]*pendingPacket),
		completed: make(map[uint16]time.Time),
	}
}

// IngestChunk processes a fragment and returns the full packet if complete
func (r *Reassembler) IngestChunk(data []byte) []byte {
	if len(data) < FragHeaderLen {
		return nil
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	// Parse Header [ID:2][Total:1][Seq:1]
	packetID := binary.BigEndian.Uint16(data[0:2])
	total := int(data[2])
	seq := int(data[3])
	payload := data[4:]

	// Check if this packet was recently completed (ignore duplicate fragments)
	if _, wasCompleted := r.completed[packetID]; wasCompleted {
		// Debug: log that we're ignoring duplicate
		// log.Debug().Uint16("pktID", packetID).Msg("Ignoring duplicate fragment (already completed)")
		return nil
	}

	// Cleanup old completed entries (keep for 30 seconds)
	now := time.Now()
	for id, completedAt := range r.completed {
		if now.Sub(completedAt) > 30*time.Second {
			delete(r.completed, id)
		}
	}

	pkt, exists := r.pending[packetID]
	if !exists {
		// Cleanup old garbage (simplified)
		if len(r.pending) > 1000 {
			r.pending = make(map[uint16]*pendingPacket)
		}
		pkt = &pendingPacket{
			Chunks:    make([][]byte, total),
			Total:     total,
			CreatedAt: time.Now(),
		}
		r.pending[packetID] = pkt
	}

	if seq < total && pkt.Chunks[seq] == nil {
		pkt.Chunks[seq] = payload
		pkt.Received++
	}

	if pkt.Received == pkt.Total {
		delete(r.pending, packetID)
		r.completed[packetID] = now // Mark as completed to ignore future duplicates
		var full []byte
		for _, chunk := range pkt.Chunks {
			full = append(full, chunk...)
		}
		return full
	}
	return nil
}

// FragmentPacket splits a large packet into small chunks with headers
func FragmentPacket(data []byte) [][]byte {
	// 1. Generate Random Packet ID
	packetID := uint16(rand.Intn(65535))

	// 2. Calculate Split
	totalLen := len(data)
	totalChunks := (totalLen + MaxChunkSize - 1) / MaxChunkSize

	// Safety check (should not happen with standard MTU)
	if totalChunks > 255 {
		totalChunks = 255
	}

	chunks := make([][]byte, totalChunks)

	for i := 0; i < totalChunks; i++ {
		start := i * MaxChunkSize
		end := start + MaxChunkSize
		if end > totalLen {
			end = totalLen
		}

		// 3. Create Payload: [Header] + [DataChunk]
		payload := make([]byte, FragHeaderLen+(end-start))

		// Write Header
		binary.BigEndian.PutUint16(payload[0:2], packetID)
		payload[2] = uint8(totalChunks)
		payload[3] = uint8(i) // Sequence Number

		// Copy Data
		copy(payload[4:], data[start:end])

		chunks[i] = payload
	}

	return chunks
}
