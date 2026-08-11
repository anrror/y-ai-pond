// Package shadow implements the device shadow protocol: reported
// (device-reported attributes) ↔ desired (cloud-desired attributes)
// → delta (auto-diff published via MQTT). It also provides hardware
// safety interlock protection — protected param keys in desired are
// dropped so they can never override edge-side safety interlocks.
//
// Design follows the three-document model:
//
//	reported  — current device state (device → cloud)
//	desired   — target state (cloud → device)
//	delta     — key-value pairs where desired differs from reported
//
// The Service orchestrates persistence (Store) and MQTT publishing
// (Reporter). Wiring to Redis, PostgreSQL, and the MQTT client is
// done by the caller.
package shadow

import (
	"context"
	"fmt"
	"log/slog"
	"time"
)

// Shadow is the full device shadow document.
type Shadow struct {
	DeviceID  string         `json:"device_id"`
	Reported  map[string]any `json:"reported"`
	Desired   map[string]any `json:"desired"`
	Delta     map[string]any `json:"delta"`
	UpdatedAt time.Time      `json:"updated_at"`
}

// Reporter publishes shadow deltas and OTA commands to the device via MQTT.
type Reporter interface {
	PublishConfigUpdate(ctx context.Context, deviceID string, delta map[string]any) error
	PublishModelUpdate(ctx context.Context, deviceID string, cmd OTACommand) error
}

// Store persists the shadow document (Redis cache + PostgreSQL backup).
type Store interface {
	GetShadow(ctx context.Context, deviceID string) (*Shadow, error)
	PutShadow(ctx context.Context, s *Shadow) error
}

// Service manages the device shadow lifecycle.
type Service struct {
	store    Store
	reporter Reporter
	log      *slog.Logger

	ota *OTAManager

	protected map[string]bool
}

// Option configures a Service.
type Option func(*Service)

// WithProtectedParams registers hardware safety interlock parameter keys
// that desired MUST NOT override (e.g. "do_threshold", "estop").
func WithProtectedParams(params []string) Option {
	return func(s *Service) {
		for _, p := range params {
			s.protected[p] = true
		}
	}
}

// WithLogger sets the structured logger.
func WithLogger(log *slog.Logger) Option {
	return func(s *Service) {
		s.log = log
	}
}

// NewService creates a Service with sensible defaults.
func NewService(store Store, rep Reporter, opts ...Option) *Service {
	s := &Service{
		store:     store,
		reporter:  rep,
		log:       slog.Default(),
		ota:       NewOTAManager(64*1024, 16*1024*1024), // 64KB chunks, 16MB max
		protected: make(map[string]bool),
	}
	for _, o := range opts {
		o(s)
	}
	return s
}

// UpdateDesired sets desired attributes (filtering protected params),
// computes delta, persists, and publishes the delta via MQTT.
func (s *Service) UpdateDesired(ctx context.Context, deviceID string, desired map[string]any) (map[string]any, error) {
	sh, err := s.getOrCreateShadow(ctx, deviceID)
	if err != nil {
		return nil, err
	}

	cleaned := s.filterProtected(desired)
	sh.Desired = cleaned
	sh.Delta = ComputeDelta(sh.Reported, sh.Desired)
	sh.UpdatedAt = time.Now()

	if err := s.store.PutShadow(ctx, sh); err != nil {
		return nil, fmt.Errorf("shadow: persist after UpdateDesired: %w", err)
	}

	if len(sh.Delta) > 0 {
		if err := s.reporter.PublishConfigUpdate(ctx, deviceID, sh.Delta); err != nil {
			s.log.Warn("shadow: publish config update failed", "device", deviceID, "error", err)
		}
	}

	return sh.Delta, nil
}

// ReportReported updates reported attributes, persists, and returns the
// new delta.
func (s *Service) ReportReported(ctx context.Context, deviceID string, reported map[string]any) (map[string]any, error) {
	sh, err := s.getOrCreateShadow(ctx, deviceID)
	if err != nil {
		return nil, err
	}

	if sh.Reported == nil {
		sh.Reported = make(map[string]any)
	}
	for k, v := range reported {
		sh.Reported[k] = v
	}
	sh.Delta = ComputeDelta(sh.Reported, sh.Desired)
	sh.UpdatedAt = time.Now()

	if err := s.store.PutShadow(ctx, sh); err != nil {
		return nil, fmt.Errorf("shadow: persist after ReportReported: %w", err)
	}

	return sh.Delta, nil
}

// Sync computes the delta and publishes it if non-empty; returns the delta.
func (s *Service) Sync(ctx context.Context, deviceID string) (map[string]any, error) {
	sh, err := s.store.GetShadow(ctx, deviceID)
	if err != nil {
		return nil, fmt.Errorf("shadow: sync get: %w", err)
	}

	delta := ComputeDelta(sh.Reported, sh.Desired)
	if len(delta) == 0 {
		return delta, nil
	}

	if err := s.reporter.PublishConfigUpdate(ctx, deviceID, delta); err != nil {
		return delta, fmt.Errorf("shadow: sync publish: %w", err)
	}
	return delta, nil
}

// getOrCreateShadow fetches the current shadow or creates an empty one.
func (s *Service) getOrCreateShadow(ctx context.Context, deviceID string) (*Shadow, error) {
	sh, err := s.store.GetShadow(ctx, deviceID)
	if err != nil {
		sh = &Shadow{
			DeviceID:  deviceID,
			Reported:  make(map[string]any),
			Desired:   make(map[string]any),
			Delta:     make(map[string]any),
			UpdatedAt: time.Now(),
		}
	}
	return sh, nil
}

// filterProtected removes any desired keys that are registered as protected
// hardware safety interlock parameters.
func (s *Service) filterProtected(desired map[string]any) map[string]any {
	cleaned := make(map[string]any, len(desired))
	for k, v := range desired {
		if s.protected[k] {
			s.log.Warn("shadow: protected param dropped from desired", "key", k)
			continue
		}
		cleaned[k] = v
	}
	return cleaned
}
