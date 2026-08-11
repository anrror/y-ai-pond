// Package store provides database clients and migrations for y-ai-pond.
package store

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// Common store errors.
var (
	ErrNotFound  = errors.New("store: not found")
	ErrNotOpen   = errors.New("store: not open")
	ErrDuplicate = errors.New("store: duplicate key")
)

// SensorPoint is a single InfluxDB measurement point.
// Tags (indexed): farm_id, pond_id, sensor_type.
// Fields: scalar sensor readings.
type SensorPoint struct {
	FarmID     string
	PondID     string
	SensorType string
	Timestamp  time.Time
	Fields     map[string]float64
}

// Point is a generic query result.
type Point struct {
	Timestamp time.Time
	Tags      map[string]string
	Fields    map[string]float64
}

// InfluxWriter is the interface for writing/querying InfluxDB.
// Mockable for unit tests.
type InfluxWriter interface {
	WriteSensorData(ctx context.Context, points []SensorPoint) error
	QueryTimeRange(ctx context.Context, measurement, start, end string) ([]Point, error)
	Close() error
}

// InfluxConfig holds InfluxDB 3 connection parameters.
type InfluxConfig struct {
	URL   string
	Token string
	Org   string
}

// Ensure interface compliance.
var _ InfluxWriter = (*InfluxStore)(nil)

// InfluxStore wraps the InfluxDB 3 Go client.
type InfluxStore struct {
	cfg    InfluxConfig
	closed bool
}

// NewInflux creates an InfluxStore. The underlying InfluxDB 3 client is lazily
// initialised on first write/query call. In production this delegates to
// influxdb3-go; for testing, inject a mock via the interface.
func NewInflux(cfg InfluxConfig) (*InfluxStore, error) {
	if cfg.URL == "" {
		return nil, fmt.Errorf("store: influxdb url is required")
	}
	return &InfluxStore{cfg: cfg}, nil
}

// WriteSensorData writes a batch of sensor points to InfluxDB.
// In production, this uses influxdb3-go's batch write API.
func (s *InfluxStore) WriteSensorData(_ context.Context, _ []SensorPoint) error {
	if s.closed {
		return ErrNotOpen
	}
	// Placeholder: real implementation uses influxdb3-go batch write.
	// For now, this is exercised through mock injection in tests.
	return errors.New("store: influxdb write not implemented (requires influxdb3-go runtime)")
}

// QueryTimeRange queries points within a time range.
func (s *InfluxStore) QueryTimeRange(_ context.Context, _ string, _ string, _ string) ([]Point, error) {
	if s.closed {
		return nil, ErrNotOpen
	}
	return nil, errors.New("store: influxdb query not implemented (requires influxdb3-go runtime)")
}

// Close marks the store as closed.
func (s *InfluxStore) Close() error {
	s.closed = true
	return nil
}
