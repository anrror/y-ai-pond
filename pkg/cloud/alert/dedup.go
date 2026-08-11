package alert

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/anrror/y-ai-pond/pkg/store"
)

// ============================================================================
// DedupKey
// ============================================================================

// DedupKey returns the deduplication key for an alert (farm+pond+type).
func DedupKey(a Alert) string {
	return fmt.Sprintf("alert:dedup:%s:%s:%s", a.FarmID, a.PondID, a.Type)
}

// ============================================================================
// Deduper
// ============================================================================

// Deduper suppresses duplicate alerts within a configurable window.
type Deduper interface {
	// Allow reports whether this alert key may fire now (not seen in window).
	Allow(ctx context.Context, key string, now time.Time) bool
}

// Compile-time interface assertions.
var (
	_ Deduper = (*RedisDeduper)(nil)
	_ Deduper = (*MemoryDeduper)(nil)
)

// ============================================================================
// RedisDeduper
// ============================================================================

// RedisDeduper uses Redis SETNX with TTL = dedup window.
type RedisDeduper struct {
	store  *store.RedisStore
	window time.Duration
}

// NewRedisDeduper creates a Redis-backed deduper. window is the dedup
// window (the TTL assigned to each key via SETNX).
func NewRedisDeduper(s *store.RedisStore, window time.Duration) *RedisDeduper {
	return &RedisDeduper{store: s, window: window}
}

// Allow returns true when the key is not present in Redis (SETNX succeeds).
func (d *RedisDeduper) Allow(ctx context.Context, key string, _ time.Time) bool {
	ttlSec := int(d.window.Seconds())
	if ttlSec < 1 {
		ttlSec = 1
	}
	ok, err := d.store.SetNX(key, ttlSec)
	if err != nil {
		// Degrade to allowing on Redis errors.
		return true
	}
	return ok
}

// ============================================================================
// MemoryDeduper — in-memory fallback when Redis is unavailable.
// ============================================================================

// MemoryDeduper is the in-memory fallback when Redis is unavailable.
type MemoryDeduper struct {
	mu      sync.Mutex
	window  time.Duration
	entries map[string]time.Time
}

// NewMemoryDeduper creates an in-memory deduper with the given window.
func NewMemoryDeduper(window time.Duration) *MemoryDeduper {
	return &MemoryDeduper{
		window:  window,
		entries: make(map[string]time.Time),
	}
}

// Allow returns false if the key was already seen within the dedup window.
func (m *MemoryDeduper) Allow(_ context.Context, key string, now time.Time) bool {
	m.mu.Lock()
	defer m.mu.Unlock()

	if last, exists := m.entries[key]; exists && now.Sub(last) < m.window {
		return false
	}
	m.entries[key] = now
	return true
}
