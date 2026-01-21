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
// Calculation:
//   - Reserve ~25 chars for ".session.domain." suffix
//   - Available for data: ~228 chars including label dots
//   - With 3 dots between 4 labels: 224 chars base32 data
//   - 224 base32 chars = 224 * 5 / 8 = 140 bytes raw
//   - Subtract 4 byte header = 136 bytes max payload
// Use 120 bytes for safety margin
const MaxChunkSize = 120

func init() {
	rand.Seed(time.Now().UnixNano())
}

// Reassembler reassembles fragmented packets
type Reassembler struct {
	pending map[uint16]*pendingPacket
	mu      sync.Mutex
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
		pending: make(map[uint16]*pendingPacket),
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
