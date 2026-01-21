package server

import (
	"sync"
	"time"

	"github.com/patrickmn/go-cache"
)

type Session struct {
	ID          string
	Queue       chan []byte   // Full QUIC packets (for backward compat)
	FragQueue   chan []byte   // Pre-fragmented chunks for DNS responses
	Reassembler *Reassembler
	LastSeen    time.Time
	mu          sync.Mutex
}

type SessionManager struct {
	store *cache.Cache
}

func NewSessionManager() *SessionManager {
	return &SessionManager{
		// 5 minute default expiration, cleanup every 10 minutes
		// Sessions are refreshed on every access via GetOrCreate
		store: cache.New(5*time.Minute, 10*time.Minute),
	}
}

func (sm *SessionManager) GetOrCreate(id string) *Session {
	if val, found := sm.store.Get(id); found {
		sess := val.(*Session)
		// Refresh TTL on every access to keep session alive
		sm.store.Set(id, sess, cache.DefaultExpiration)
		sess.mu.Lock()
		sess.LastSeen = time.Now()
		sess.mu.Unlock()
		return sess
	}

	sess := &Session{
		ID:          id,
		Queue:       make(chan []byte, 2000), // Full packets (legacy)
		FragQueue:   make(chan []byte, 4000), // Fragments for DNS responses
		Reassembler: NewReassembler(),
		LastSeen:    time.Now(),
	}
	sm.store.Set(id, sess, cache.DefaultExpiration)
	return sess
}
