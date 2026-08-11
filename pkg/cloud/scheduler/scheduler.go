// Package scheduler implements multi-device cluster scheduling, health
// monitoring, and batch command fan-out for y-ai-pond's cloud tier.
// It groups devices into farm→pond→device trees, fans out batch feeding
// and aeration commands via MQTT, monitors device heartbeats, and
// provides round-robin rotation for device duty cycling.
package scheduler

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"
)

// Device is a minimal device view for scheduling (grouped by farm/pond).
type Device struct {
	ID            string
	FarmID        string
	PondID        string
	Type          string // "feeding_motor", "aerator", "sensor_node", ...
	Status        string // "online", "offline", ...
	LastHeartbeat time.Time
}

// Group is the farm→pond→device tree node.
type Group struct {
	FarmID string
	Ponds  map[string][]Device // pondID -> devices
}

// Command is a fan-out command targeting a set of devices.
type Command struct {
	FarmID  string
	PondID  string
	Type    string // "feeding_start", "feeding_stop", "aerator_on", "aerator_off"
	Payload map[string]any
}

// Commander publishes commands to devices (MQTT fan-out).
type Commander interface {
	PublishCommand(ctx context.Context, deviceID, topic string, payload []byte) error
}

// AlertReporter emits offline/health alerts.
type AlertReporter interface {
	ReportOffline(ctx context.Context, device Device, reason string) error
}

// Result is one device command outcome.
type Result struct {
	DeviceID string
	PondID   string
	Status   string // "sent" | "skipped" | "failed"
	Error    string `json:",omitempty"`
}

// Scheduler groups devices and fans out batch commands.
type Scheduler struct {
	commander Commander
	alerts    AlertReporter
	devices   []Device
	log       *slog.Logger
}

// NewScheduler creates a Scheduler that uses cmd for MQTT fan-out and
// alerts for health notifications.
func NewScheduler(cmd Commander, alerts AlertReporter, log *slog.Logger) *Scheduler {
	if log == nil {
		log = slog.Default()
	}
	return &Scheduler{commander: cmd, alerts: alerts, log: log}
}

// SetDevices replaces the device list used for batch operations.
func (s *Scheduler) SetDevices(devices []Device) {
	s.devices = devices
}

// BuildTree groups devices into farm→pond→device.
func (s *Scheduler) BuildTree(devices []Device) map[string]Group {
	tree := make(map[string]Group)
	for _, d := range devices {
		g, ok := tree[d.FarmID]
		if !ok {
			g = Group{FarmID: d.FarmID, Ponds: make(map[string][]Device)}
		}
		g.Ponds[d.PondID] = append(g.Ponds[d.PondID], d)
		tree[d.FarmID] = g
	}
	return tree
}

// cmdTopic maps a command Type to the MQTT topic suffix and returns the
// full cloud/{farmID}/{pondID}/cmd/{suffix} topic.
func cmdTopic(farmID, pondID, typ string) string {
	var suffix string
	switch typ {
	case "feeding_start":
		suffix = "feeding/start"
	case "feeding_stop":
		suffix = "feeding/stop"
	case "aerator_on":
		suffix = "aerator/on"
	case "aerator_off":
		suffix = "aerator/off"
	default:
		suffix = typ
	}
	return fmt.Sprintf("cloud/%s/%s/cmd/%s", farmID, pondID, suffix)
}

// deviceInPonds returns true when the device belongs to one of the given ponds.
func deviceInPonds(d Device, ponds map[string]bool) bool {
	return ponds[d.PondID]
}

var _ = deviceInPonds //nolint:unused // retained for future batch validation

// BatchFeeding fans out a feeding command to the feeding devices of the
// given ponds. Offline devices are skipped; a failed publish for one
// device does not block the rest.
func (s *Scheduler) BatchFeeding(ctx context.Context, farmID string, pondIDs []string, payload map[string]any) []Result {
	return s.batch(ctx, farmID, pondIDs, "feeding_start", payload, func(d Device) bool {
		return d.Type == "feeding_motor"
	})
}

// BatchAeration fans out aerator on/off to the aeration devices of the
// given ponds.
func (s *Scheduler) BatchAeration(ctx context.Context, farmID string, pondIDs []string, on bool) []Result {
	typ := "aerator_on"
	if !on {
		typ = "aerator_off"
	}
	return s.batch(ctx, farmID, pondIDs, typ, nil, func(d Device) bool {
		return d.Type == "aerator"
	})
}

// batch executes a fan-out command across the given ponds, filtering
// devices by match and constructing MQTT topics of the form
// cloud/{farmID}/{pondID}/cmd/{type}.
func (s *Scheduler) batch(ctx context.Context, farmID string, pondIDs []string, typ string, payload map[string]any, match func(Device) bool) []Result {
	pondSet := make(map[string]bool, len(pondIDs))
	for _, id := range pondIDs {
		pondSet[id] = true
	}

	var results []Result
	for _, d := range s.devices {
		if d.FarmID != farmID || !pondSet[d.PondID] || !match(d) {
			continue
		}
		if d.Status != "online" {
			results = append(results, Result{
				DeviceID: d.ID,
				PondID:   d.PondID,
				Status:   "skipped",
			})
			continue
		}
		topic := cmdTopic(farmID, d.PondID, typ)
		payloadJSON, err := json.Marshal(payload)
		if err != nil {
			results = append(results, Result{
				DeviceID: d.ID,
				PondID:   d.PondID,
				Status:   "failed",
				Error:    err.Error(),
			})
			continue
		}
		if err := s.commander.PublishCommand(ctx, d.ID, topic, payloadJSON); err != nil {
			results = append(results, Result{
				DeviceID: d.ID,
				PondID:   d.PondID,
				Status:   "failed",
				Error:    err.Error(),
			})
			continue
		}
		results = append(results, Result{
			DeviceID: d.ID,
			PondID:   d.PondID,
			Status:   "sent",
		})
	}
	return results
}
