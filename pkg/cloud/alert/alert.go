// Package alert implements the real-time alert engine for y-ai-pond.
// It evaluates sensor snapshots against configurable thresholds, applies
// deduplication and escalation, and optionally detects anomalies via a
// pure-Go STL-style residual detector. Alerts are delivered through
// pluggable notifier channels (log, webhook, fan-out).
package alert

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"
)

// ============================================================================
// Types
// ============================================================================

// Level is the alert severity.
type Level string

const (
	LevelInfo     Level = "INFO"
	LevelWarning  Level = "WARNING"
	LevelCritical Level = "CRITICAL"
)

// Alert is a generated alert event.
type Alert struct {
	ID        string    `json:"id"`
	FarmID    string    `json:"farm_id"`
	PondID    string    `json:"pond_id"`
	Type      string    `json:"type"` // e.g. "do_low", "ph_high", "temp_high", "nh3_high", "anomaly"
	Level     Level     `json:"level"`
	Message   string    `json:"message"`
	Value     float64   `json:"value"`
	Timestamp time.Time `json:"timestamp"`
}

// SensorSnapshot carries the water-quality readings evaluated by the engine.
type SensorSnapshot struct {
	PH   float64
	DO   float64
	Temp float64
	NH3  float64
}

// Config holds thresholds and dedup/escalation parameters.
type Config struct {
	PHMin              float64
	PHMax              float64
	DOMin              float64
	TempMax            float64
	NH3Max             float64
	DedupWindow        time.Duration
	EscalationDuration time.Duration
	RateLimit          time.Duration
	AnomalySigma       float64
}

// DefaultConfig returns the recommended default thresholds for pond water quality.
func DefaultConfig() Config {
	return Config{
		PHMin:              6.5,
		PHMax:              8.5,
		DOMin:              4.0,
		TempMax:            35.0,
		NH3Max:             0.5,
		DedupWindow:        60 * time.Second,
		EscalationDuration: 30 * time.Minute,
		RateLimit:          time.Second,
		AnomalySigma:       3.0,
	}
}

// ============================================================================
// Notifier
// ============================================================================

// Notifier delivers alerts to external channels.
type Notifier interface {
	Notify(ctx context.Context, a Alert) error
}

// LogNotifier writes alerts to slog at WARN level.
type LogNotifier struct {
	log *slog.Logger
}

// NewLogNotifier creates a LogNotifier. If log is nil, slog.Default is used.
func NewLogNotifier(log *slog.Logger) *LogNotifier {
	if log == nil {
		log = slog.Default()
	}
	return &LogNotifier{log: log}
}

// Notify logs the alert as a structured WARN record.
func (n *LogNotifier) Notify(_ context.Context, a Alert) error {
	n.log.Warn("alert",
		"id", a.ID,
		"farm_id", a.FarmID,
		"pond_id", a.PondID,
		"type", a.Type,
		"level", a.Level,
		"message", a.Message,
		"value", a.Value,
	)
	return nil
}

// WebhookNotifier POSTs the alert as JSON to a webhook URL (non-blocking,
// best-effort delivery).
type WebhookNotifier struct {
	url    string
	client *http.Client
}

// NewWebhookNotifier creates a WebhookNotifier.
func NewWebhookNotifier(url string) *WebhookNotifier {
	return &WebhookNotifier{
		url:    url,
		client: &http.Client{Timeout: 5 * time.Second},
	}
}

// Notify sends the alert JSON to the configured webhook URL. Errors are
// logged but not returned — delivery is best-effort.
func (n *WebhookNotifier) Notify(ctx context.Context, a Alert) error {
	body, err := json.Marshal(a)
	if err != nil {
		return fmt.Errorf("webhook marshal: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, n.url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("webhook request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := n.client.Do(req)
	if err != nil {
		return fmt.Errorf("webhook post: %w", err)
	}
	_ = resp.Body.Close()
	return nil
}

// MultiNotifier fans out notifications to multiple notifiers. Errors from
// individual notifiers are logged and not propagated.
type MultiNotifier struct {
	notifiers []Notifier
	log       *slog.Logger
}

// NewMultiNotifier creates a MultiNotifier. If log is nil, slog.Default is used.
func NewMultiNotifier(log *slog.Logger, notifiers ...Notifier) *MultiNotifier {
	if log == nil {
		log = slog.Default()
	}
	return &MultiNotifier{notifiers: notifiers, log: log}
}

// Notify fans out the alert to all registered notifiers.
func (m *MultiNotifier) Notify(ctx context.Context, a Alert) error {
	for _, n := range m.notifiers {
		if err := n.Notify(ctx, a); err != nil {
			m.log.Warn("alert: notifier error", "error", err)
		}
	}
	return nil
}

// ============================================================================
// Engine
// ============================================================================

// Engine evaluates sensor snapshots against thresholds and produces alerts.
type Engine struct {
	cfg        Config
	deduper    Deduper
	escalator  *Escalator
	detector   *AnomalyDetector
	notifier   Notifier
	rateMu     sync.Mutex
	lastNotify time.Time
}

// NewEngine creates an alert Engine with the given configuration and
// optional components. All parameters may be nil for defaults: a nil
// deduper allows all alerts, a nil escalator skips escalation, a nil
// detector skips anomaly detection, and a nil notifier falls back to
// log-only delivery.
func NewEngine(cfg Config, deduper Deduper, escalator *Escalator, detector *AnomalyDetector, notifier Notifier) *Engine {
	if notifier == nil {
		notifier = NewLogNotifier(nil)
	}
	return &Engine{
		cfg:       cfg,
		deduper:   deduper,
		escalator: escalator,
		detector:  detector,
		notifier:  notifier,
	}
}

// Evaluate processes a sensor snapshot and returns any alerts generated.
// It applies threshold rules, dedup, escalation, and rate limiting.
func (e *Engine) Evaluate(ctx context.Context, farmID, pondID string, snap SensorSnapshot) []Alert {
	alerts := thresholdAlert(farmID, pondID, snap, e.cfg)

	if e.detector != nil {
		if a, err := e.detector.Detect(ctx, farmID, pondID, snap.DO, time.Now()); err == nil && a != nil {
			alerts = append(alerts, *a)
		}
	}

	now := time.Now()
	deduped := make([]Alert, 0, len(alerts))
	for _, a := range alerts {
		key := DedupKey(a)
		if e.deduper == nil || e.deduper.Allow(ctx, key, now) {
			deduped = append(deduped, a)
		}
	}
	alerts = deduped

	if e.escalator != nil {
		for i := range alerts {
			alerts[i] = e.escalator.Escalate(alerts[i], now)
		}
	}

	for i := range alerts {
		id := alertID(alerts[i].Type)
		alerts[i].ID = id
		alerts[i].Timestamp = now
		e.notify(ctx, alerts[i])
	}

	return alerts
}

// notify delivers a single alert through the configured notifier,
// respecting the rate limit. If the rate limit is exceeded the alert is
// dropped and a warning is logged.
func (e *Engine) notify(ctx context.Context, a Alert) {
	e.rateMu.Lock()
	elapsed := time.Since(e.lastNotify)
	if elapsed < e.cfg.RateLimit {
		e.rateMu.Unlock()
		slog.Warn("alert: rate limited, dropping", "type", a.Type, "level", a.Level)
		return
	}
	e.lastNotify = time.Now()
	e.rateMu.Unlock()

	go func() {
		defer func() {
			if r := recover(); r != nil {
				slog.Error("alert: notifier panic", "panic", r)
			}
		}()
		if err := e.notifier.Notify(ctx, a); err != nil {
			slog.Warn("alert: notify error", "error", err)
		}
	}()
}

// ============================================================================
// Threshold logic
// ============================================================================

// thresholdAlert produces alerts for sensor values outside configurable
// ranges. It generates one alert per violated threshold.
func thresholdAlert(farmID, pondID string, snap SensorSnapshot, cfg Config) []Alert {
	var alerts []Alert
	now := time.Now()

	if snap.DO < cfg.DOMin {
		alerts = append(alerts, Alert{
			FarmID:    farmID,
			PondID:    pondID,
			Type:      "do_low",
			Level:     LevelCritical,
			Message:   fmt.Sprintf("Dissolved oxygen %.2f mg/L below threshold %.1f mg/L", snap.DO, cfg.DOMin),
			Value:     snap.DO,
			Timestamp: now,
		})
	}

	if snap.PH < cfg.PHMin {
		alerts = append(alerts, Alert{
			FarmID:    farmID,
			PondID:    pondID,
			Type:      "ph_low",
			Level:     LevelCritical,
			Message:   fmt.Sprintf("pH %.2f below threshold %.1f", snap.PH, cfg.PHMin),
			Value:     snap.PH,
			Timestamp: now,
		})
	}

	if snap.PH > cfg.PHMax {
		alerts = append(alerts, Alert{
			FarmID:    farmID,
			PondID:    pondID,
			Type:      "ph_high",
			Level:     LevelCritical,
			Message:   fmt.Sprintf("pH %.2f above threshold %.1f", snap.PH, cfg.PHMax),
			Value:     snap.PH,
			Timestamp: now,
		})
	}

	if snap.Temp > cfg.TempMax {
		alerts = append(alerts, Alert{
			FarmID:    farmID,
			PondID:    pondID,
			Type:      "temp_high",
			Level:     LevelWarning,
			Message:   fmt.Sprintf("Temperature %.2f °C above threshold %.1f °C", snap.Temp, cfg.TempMax),
			Value:     snap.Temp,
			Timestamp: now,
		})
	}

	if snap.NH3 > cfg.NH3Max {
		alerts = append(alerts, Alert{
			FarmID:    farmID,
			PondID:    pondID,
			Type:      "nh3_high",
			Level:     LevelCritical,
			Message:   fmt.Sprintf("Ammonia nitrogen %.3f mg/L above threshold %.1f mg/L", snap.NH3, cfg.NH3Max),
			Value:     snap.NH3,
			Timestamp: now,
		})
	}

	return alerts
}

// ============================================================================
// Helpers
// ============================================================================

// alertID generates a short unique alert identifier.
func alertID(alertType string) string {
	var b [6]byte
	_, _ = rand.Read(b[:])
	return "alert-" + alertType + "-" + hex.EncodeToString(b[:])
}
