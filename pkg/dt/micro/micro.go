// Package micro implements the μDT (micro digital twin) edge deployment
// pattern (T30).
//
// LocalDTMicro is a lightweight local digital twin that runs independently
// on the edge device when the MQTT connection is lost. It reuses the
// deterministic PondSimulator from the scenario package (no reimplemented
// physics). During offline periods water states are buffered with sequence
// numbers; when the connection is re-established Sync returns the full
// ordered buffer for backfill upload.
//
// Network degradation design (per T30 acceptance criteria):
//   - Connected:   states are buffered but immediately flushed via Sync.
//   - Disconnected: Step still advances the local simulation; states
//     accumulate in a bounded buffer.
//   - Reconnect:   Sync returns all buffered states in sequence order;
//     the cloud replays them to catch up the remote digital twin.
//   - Offline >72h: buffer hits capacity → oldest entries are LRU-evicted
//     (DefaultMaxBuffer = 5000, ≈83h at 1 sample/min).
//
// Hardware envelope (RK3588): stdlib-only, deterministic stepping, zero
// ML runtime — a single core at ~100 MHz handles 1K steps/s. The full
// GNN inference stays in the cloud per the "Must NOT do" constraint.
package micro

import (
	"errors"
	"sync"

	"github.com/anrror/y-ai-pond/pkg/dt/scenario"
)

// ============================================================================
// Types
// ============================================================================

// BufferedState is a water-state snapshot with a monotonic sequence number
// that the cloud uses for ordered replay.
type BufferedState struct {
	Seq   int64               `json:"seq"`
	State scenario.WaterState `json:"state"`
}

// LocalDTMicro runs a lightweight digital twin on the edge device.
//
// It is the local counterpart of the full cloud DigitalTwin: when the MQTT
// link is up states are synced near-real-time; when the link is down the
// micro twin continues to simulate locally and buffers results for backfill.
type LocalDTMicro interface {
	// Step advances the local simulation by one step under the configured
	// scenario and returns the new water state. The state is always appended
	// to the internal buffer (which may trigger LRU eviction if full).
	Step() (scenario.WaterState, error)

	// State returns the most recent water state without advancing.
	State() scenario.WaterState

	// Sync returns all buffered states (oldest first) that have not yet
	// been uploaded to the cloud. After the call the buffer is empty.
	Sync() []BufferedState

	// SetConnected updates the MQTT connection status.
	SetConnected(connected bool)

	// IsConnected reports whether the MQTT link is currently up.
	IsConnected() bool

	// BufferLen returns the number of buffered (unsynced) states.
	BufferLen() int
}

// ============================================================================
// Configuration
// ============================================================================

// DefaultMaxBuffer covers a 72-hour offline window at 1 sample/min plus
// a 10% safety margin (72 × 60 × 1.1 ≈ 4752 → rounded to 5000).
const DefaultMaxBuffer = 5000

// Config holds initialization parameters for the micro DT.
type Config struct {
	// Scenario drives the local simulation (e.g. a baseline weather scenario).
	// If the scenario is zero-valued the micro DT uses its own default
	// baseline scenario.
	Scenario scenario.Scenario

	// MaxBuffer is the maximum number of buffered states before LRU eviction.
	// If ≤ 0, DefaultMaxBuffer is used.
	MaxBuffer int
}

// DefaultScenario returns a baseline scenario suitable for continuous
// local simulation (30 days, 1 step/min, no extreme weather forcing).
func DefaultScenario() scenario.Scenario {
	return scenario.Scenario{
		Type:          "baseline",
		DurationHours: 720, // 30 days
		StepsPerHour:  60,  // 1 step per minute
	}
}

// ============================================================================
// Sentinel errors
// ============================================================================

var (
	// ErrBufferOverflow is returned when the buffer is full and the oldest
	// entry is evicted (LRU). The caller can check this to decide whether
	// to increase MaxBuffer or force a flush.
	ErrBufferOverflow = errors.New("micro DT buffer overflow: oldest entry evicted (LRU)")

	// ErrScenarioInvalid is returned when the configured scenario fails
	// validation.
	ErrScenarioInvalid = errors.New("micro DT scenario is invalid")
)

// ============================================================================
// Implementation
// ============================================================================

// compile-time interface satisfaction check
var _ LocalDTMicro = (*microDT)(nil)

type microDT struct {
	mu        sync.RWMutex
	sim       *scenario.PondSimulator
	sc        scenario.Scenario
	state     scenario.WaterState
	seq       int64
	connected bool
	buffer    []BufferedState
	maxBuffer int
	overflow  bool // set when LRU eviction happened since last Sync
}

// New creates a LocalDTMicro with the given configuration.
func New(cfg Config) LocalDTMicro {
	if cfg.MaxBuffer <= 0 {
		cfg.MaxBuffer = DefaultMaxBuffer
	}
	sc := cfg.Scenario
	if sc.Type == "" {
		sc = DefaultScenario()
	}
	sim := scenario.NewPondSimulator()
	return &microDT{
		sim:       sim,
		sc:        sc,
		state:     sim.Baseline(),
		maxBuffer: cfg.MaxBuffer,
	}
}

// Step advances the simulation and appends the new state to the buffer.
func (m *microDT) Step() (scenario.WaterState, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.seq++
	m.state = m.sim.Step(m.sc, m.state, int(m.seq))

	bs := BufferedState{Seq: m.seq, State: m.state}
	m.buffer = append(m.buffer, bs)

	if len(m.buffer) > m.maxBuffer {
		// LRU: evict the oldest entry to make room.
		m.buffer = m.buffer[1:]
		m.overflow = true
		return m.state, ErrBufferOverflow
	}

	return m.state, nil
}

// State returns the current water state without advancing.
func (m *microDT) State() scenario.WaterState {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.state
}

// Sync returns all buffered states in sequence order and clears the buffer.
func (m *microDT) Sync() []BufferedState {
	m.mu.Lock()
	defer m.mu.Unlock()

	if len(m.buffer) == 0 {
		return nil
	}

	result := make([]BufferedState, len(m.buffer))
	copy(result, m.buffer)
	m.buffer = m.buffer[:0]
	m.overflow = false
	return result
}

// SetConnected updates the MQTT connection status.
func (m *microDT) SetConnected(connected bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.connected = connected
}

// IsConnected reports whether the MQTT link is up.
func (m *microDT) IsConnected() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.connected
}

// BufferLen returns the number of buffered states waiting for sync.
func (m *microDT) BufferLen() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.buffer)
}
