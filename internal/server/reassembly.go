package server

import (
	"encoding/binary"
	"sync"
	"time"
)

type Reassembler struct {
	pending   map[uint16]*PendingPacket
	completed map[uint16]time.Time // Track recently completed packet IDs to ignore duplicates
	mu        sync.Mutex
}

type PendingPacket struct {
	Chunks    [][]byte
	Total     int
	Received  int
	CreatedAt time.Time
}

func NewReassembler() *Reassembler {
	return &Reassembler{
		pending:   make(map[uint16]*PendingPacket),
		completed: make(map[uint16]time.Time),
	}
}

// IngestChunk returns FULL PACKET if ready, or nil
func (r *Reassembler) IngestChunk(data []byte) []byte {
	if len(data) < 4 {
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
			r.pending = make(map[uint16]*PendingPacket)
		}
		pkt = &PendingPacket{
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
