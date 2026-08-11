package scheduler

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

const (
	// DefaultHeartbeatTimeout is the default heartbeat stale threshold.
	DefaultHeartbeatTimeout = 120 * time.Second
)

// HealthMonitor tracks device heartbeats and flags stale devices.
type HealthMonitor struct {
	heartbeatTimeout time.Duration
	now              func() time.Time
	offlineMu        sync.Mutex
	offlineReported  map[string]bool // deviceID -> already reported
	log              *slog.Logger
}

// NewHealthMonitor creates a HealthMonitor with the given heartbeat
// timeout. If timeout is <= 0, DefaultHeartbeatTimeout is used.
func NewHealthMonitor(timeout time.Duration, log *slog.Logger) *HealthMonitor {
	if timeout <= 0 {
		timeout = DefaultHeartbeatTimeout
	}
	if log == nil {
		log = slog.Default()
	}
	return &HealthMonitor{
		heartbeatTimeout: timeout,
		now:              time.Now,
		offlineReported:  make(map[string]bool),
		log:              log,
	}
}

// setNow replaces the clock (for tests).
func (m *HealthMonitor) setNow(fn func() time.Time) {
	m.now = fn
}

// Check marks devices whose LastHeartbeat is older than the timeout as
// OFFLINE and reports an offline alert via alerts (once per device per
// offline episode). Returns the updated device list with Offline status.
func (m *HealthMonitor) Check(ctx context.Context, devices []Device, alerts AlertReporter) []Device {
	m.offlineMu.Lock()
	defer m.offlineMu.Unlock()

	now := m.now()
	out := make([]Device, len(devices))
	copy(out, devices)

	for i := range out {
		d := &out[i]
		if d.LastHeartbeat.IsZero() {
			continue
		}
		elapsed := now.Sub(d.LastHeartbeat)
		if elapsed > m.heartbeatTimeout {
			d.Status = "offline"
			if !m.offlineReported[d.ID] {
				m.log.Warn("device heartbeat stale, marking offline",
					"device_id", d.ID,
					"farm_id", d.FarmID,
					"pond_id", d.PondID,
					"last_heartbeat", d.LastHeartbeat,
					"elapsed", elapsed,
				)
				if alerts != nil {
					_ = alerts.ReportOffline(ctx, *d, "heartbeat timeout exceeded")
				}
				m.offlineReported[d.ID] = true
			}
		} else {
			// Device is back online (or never went offline), clear report flag.
			if m.offlineReported[d.ID] {
				delete(m.offlineReported, d.ID)
			}
		}
	}
	return out
}
