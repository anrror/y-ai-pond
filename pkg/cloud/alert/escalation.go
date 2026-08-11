package alert

import (
	"sync"
	"time"
)

// ============================================================================
// Escalator
// ============================================================================

// Escalator promotes WARNING alerts that persist beyond the configured
// duration to CRITICAL. It tracks per-key first-seen times.
type Escalator struct {
	mu        sync.Mutex
	duration  time.Duration
	now       func() time.Time
	firstSeen map[string]time.Time
}

// NewEscalator creates an Escalator with the given duration. For testability
// the clock is injected via now; pass nil to default to time.Now.
func NewEscalator(duration time.Duration, now func() time.Time) *Escalator {
	if now == nil {
		now = time.Now
	}
	return &Escalator{
		duration:  duration,
		now:       now,
		firstSeen: make(map[string]time.Time),
	}
}

// Escalate returns the updated alert. If the alert is WARNING and its key
// has been in WARNING state for >= duration, the level is promoted to
// CRITICAL and the message is prefixed with "[ESCALATED]".
func (e *Escalator) Escalate(a Alert, now time.Time) Alert {
	if a.Level != LevelWarning {
		return a
	}

	key := DedupKey(a)
	e.mu.Lock()
	first, exists := e.firstSeen[key]
	if !exists {
		e.firstSeen[key] = now
		e.mu.Unlock()
		return a
	}
	e.mu.Unlock()

	if now.Sub(first) >= e.duration {
		escalated := a
		escalated.Level = LevelCritical
		escalated.Message = "[ESCALATED] " + a.Message
		return escalated
	}
	return a
}
