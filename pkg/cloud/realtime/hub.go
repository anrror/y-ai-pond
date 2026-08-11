// Package realtime provides SSE and WebSocket real-time data streaming
// with room-based pub/sub isolation grouped by farm_id and pond_id.
//
// Architecture:
//
//	Hub (room-based pub/sub)
//	  ├── SSE handler (GET /api/v1/stream/sensors, /api/v1/stream/alerts)
//	  └── WebSocket handler (GET /ws/dashboard)
//
// Room naming convention:
//   - sensor:{farmID}:{pondID}  — sensor data for a specific pond
//   - alert:{farmID}            — alert events for a specific farm
//   - dashboard:{farmID}        — bidirectional dashboard for a specific farm
package realtime

import (
	"context"
	"sync"

	"github.com/anrror/y-ai-pond/pkg/cloud/alert"
)

// SensorEvent is published when new sensor data arrives from edge devices.
type SensorEvent struct {
	FarmID     string  `json:"farm_id"`
	PondID     string  `json:"pond_id"`
	SensorType string  `json:"sensor_type"`
	Value      float64 `json:"value"`
	Timestamp  string  `json:"timestamp"` // RFC 3339
}

// Subscriber represents a connected client receiving events on a channel.
type Subscriber struct {
	ID     string
	Events chan any
}

// Hub manages room-based pub/sub for real-time event streaming.
// Each room maps to a set of subscriber channels. Publish is non-blocking —
// when a subscriber's channel is full the event is dropped silently.
type Hub struct {
	mu    sync.RWMutex
	rooms map[string]map[chan any]struct{} // room -> set of subscriber channels
}

// NewHub creates a Hub ready for use.
func NewHub() *Hub {
	return &Hub{
		rooms: make(map[string]map[chan any]struct{}),
	}
}

// Subscribe registers a subscriber to one or more rooms and returns the
// subscriber's event channel along with an unsubscribe function. The returned
// function removes the subscriber from all subscribed rooms and closes the
// channel. It is safe to call the unsubscribe function multiple times.
func (h *Hub) Subscribe(subID string, rooms ...string) (*Subscriber, func()) {
	ch := make(chan any, 256)

	h.mu.Lock()
	for _, r := range rooms {
		if h.rooms[r] == nil {
			h.rooms[r] = make(map[chan any]struct{})
		}
		h.rooms[r][ch] = struct{}{}
	}
	h.mu.Unlock()

	unsub := func() {
		h.mu.Lock()
		for _, r := range rooms {
			delete(h.rooms[r], ch)
			if len(h.rooms[r]) == 0 {
				delete(h.rooms, r)
			}
		}
		h.mu.Unlock()
		// Drain and close the channel. Since we already removed it from
		// rooms, no new events will be sent; drain remaining ones so no
		// goroutine blocks on a send to a closed channel.
		for {
			select {
			case <-ch:
			default:
				close(ch)
				return
			}
		}
	}

	return &Subscriber{ID: subID, Events: ch}, unsub
}

// PublishSensor delivers a sensor event to all subscribers of the sensor room
// for the given farm and pond. The event is marshalled to JSON by consumers.
func (h *Hub) PublishSensor(farmID, pondID string, ev SensorEvent) {
	room := sensorRoom(farmID, pondID)
	h.publish(room, ev)
}

// PublishAlert delivers an alert event to all subscribers of the alert room
// for the alert's farm. The alert.Alert is passed through directly so consumers
// can marshal it.
func (h *Hub) PublishAlert(a alert.Alert) {
	room := alertRoom(a.FarmID)
	h.publish(room, a)
}

// PublishDashboard delivers an event to all subscribers of the dashboard room
// for the given farm.
func (h *Hub) PublishDashboard(farmID string, ev any) {
	room := dashboardRoom(farmID)
	h.publish(room, ev)
}

// Publish sends an arbitrary event to a raw room name. Prefer the typed
// PublishSensor / PublishAlert / PublishDashboard methods.
func (h *Hub) Publish(room string, ev any) {
	h.publish(room, ev)
}

// publish delivers an event to all subscribers in a room. Non-blocking:
// when a subscriber's buffer is full the event is dropped silently.
func (h *Hub) publish(room string, ev any) {
	h.mu.RLock()
	subs := h.rooms[room]
	// Snapshot channels so we don't hold the lock during send.
	chs := make([]chan any, 0, len(subs))
	for ch := range subs {
		chs = append(chs, ch)
	}
	h.mu.RUnlock()

	for _, ch := range chs {
		select {
		case ch <- ev:
		default:
			// Subscriber channel full — drop event to avoid blocking
			// the publisher (e.g. MQTT handler goroutine).
		}
	}
}

// SubscriberCount returns the number of subscribers in a room. For testing.
func (h *Hub) SubscriberCount(room string) int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.rooms[room])
}

// SensorRoom returns the room key for sensor data on a specific farm+pond.
func SensorRoom(farmID, pondID string) string { return sensorRoom(farmID, pondID) }

// AlertRoom returns the room key for alerts on a specific farm.
func AlertRoom(farmID string) string { return alertRoom(farmID) }

// DashboardRoom returns the room key for the dashboard on a specific farm.
func DashboardRoom(farmID string) string { return dashboardRoom(farmID) }

func sensorRoom(farmID, pondID string) string { return "sensor:" + farmID + ":" + pondID }
func alertRoom(farmID string) string          { return "alert:" + farmID }
func dashboardRoom(farmID string) string      { return "dashboard:" + farmID }

// Ensure Hub implements io.Closer-compatible pattern via Shutdown.
func (h *Hub) Shutdown(_ context.Context) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, subs := range h.rooms {
		for ch := range subs {
			close(ch)
		}
	}
	h.rooms = make(map[string]map[chan any]struct{})
	return nil
}
