package scheduler

import "sync"

// Rotator alternates which device is active for equalization (e.g.
// aerators). It provides round-robin indexing across a pool of devices
// keyed by an arbitrary string.
type Rotator struct {
	mu    sync.Mutex
	state map[string]int
}

// NewRotator creates a Rotator with an empty state.
func NewRotator() *Rotator {
	return &Rotator{state: make(map[string]int)}
}

// Next returns the index of the next device to activate for the given
// key, advancing the round-robin cursor. If poolSize <= 0, it returns 0.
func (r *Rotator) Next(key string, poolSize int) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	if poolSize <= 0 {
		return 0
	}
	idx := r.state[key]
	r.state[key] = (idx + 1) % poolSize
	return idx
}
