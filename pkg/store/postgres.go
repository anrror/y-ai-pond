package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PgxPool abstracts pgxpool methods for testability.
// Compatible with both *pgxpool.Pool (production) and pgxmock.Pool (testing).
type PgxPool interface {
	Ping(ctx context.Context) error
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	Close()
}

// PostgresStore manages the PostgreSQL connection pool and typed operations.
type PostgresStore struct {
	pool PgxPool
}

// Device represents a registered edge device.
type Device struct {
	ID              string    `json:"id"`
	FarmID          string    `json:"farm_id"`
	PondID          string    `json:"pond_id"`
	Type            string    `json:"type"`
	Status          string    `json:"status"`
	FirmwareVersion string    `json:"firmware_version"`
	LastHeartbeat   time.Time `json:"last_heartbeat"`
}

// FeedingLog records a feeding event.
type FeedingLog struct {
	ID           string          `json:"id"`
	PondID       string          `json:"pond_id"`
	Speed        float64         `json:"speed"`
	Duration     int             `json:"duration"`
	DecisionJSON json.RawMessage `json:"decision_json"`
	CreatedAt    time.Time       `json:"created_at"`
}

// NewPostgres creates a real PostgresStore backed by pgxpool.
func NewPostgres(ctx context.Context, dsn string) (*PostgresStore, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("store: parse postgres dsn: %w", err)
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("store: create postgres pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("store: ping postgres: %w", err)
	}
	return &PostgresStore{pool: pool}, nil
}

// NewPostgresWithPool creates a PostgresStore from an existing PgxPool (for testing).
func NewPostgresWithPool(pool PgxPool) *PostgresStore {
	return &PostgresStore{pool: pool}
}

// Pool returns the underlying pgx pool. Returns nil when the store is not open.
// Use for dependency injection into the MQTT gateway.
func (p *PostgresStore) Pool() PgxPool { return p.pool }

// Ping checks the database connection.
func (p *PostgresStore) Ping(ctx context.Context) error {
	if p.pool == nil {
		return ErrNotOpen
	}
	return p.pool.Ping(ctx)
}

// UpsertDevice inserts or updates a device record.
func (p *PostgresStore) UpsertDevice(ctx context.Context, d Device) error {
	if p.pool == nil {
		return ErrNotOpen
	}
	_, err := p.pool.Exec(ctx,
		`INSERT INTO devices (id, farm_id, pond_id, type, status, firmware_version, last_heartbeat)
		 VALUES ($1, $2, $3, $4, $5, $6, $7)
		 ON CONFLICT (id) DO UPDATE SET
		   farm_id = EXCLUDED.farm_id,
		   pond_id = EXCLUDED.pond_id,
		   type = EXCLUDED.type,
		   status = EXCLUDED.status,
		   firmware_version = EXCLUDED.firmware_version,
		   last_heartbeat = EXCLUDED.last_heartbeat`,
		d.ID, d.FarmID, d.PondID, d.Type, d.Status, d.FirmwareVersion, d.LastHeartbeat,
	)
	if err != nil {
		return fmt.Errorf("store: upsert device: %w", err)
	}
	return nil
}

// GetDevice retrieves a single device by ID.
func (p *PostgresStore) GetDevice(ctx context.Context, id string) (Device, error) {
	if p.pool == nil {
		return Device{}, ErrNotOpen
	}
	var d Device
	err := p.pool.QueryRow(ctx,
		`SELECT id, farm_id, pond_id, type, status, firmware_version, last_heartbeat
		 FROM devices WHERE id = $1`, id,
	).Scan(&d.ID, &d.FarmID, &d.PondID, &d.Type, &d.Status, &d.FirmwareVersion, &d.LastHeartbeat)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Device{}, fmt.Errorf("store: %w", ErrNotFound)
		}
		return Device{}, fmt.Errorf("store: get device: %w", err)
	}
	return d, nil
}

// InsertFeedingLog records a new feeding event.
func (p *PostgresStore) InsertFeedingLog(ctx context.Context, fl FeedingLog) error {
	if p.pool == nil {
		return ErrNotOpen
	}
	_, err := p.pool.Exec(ctx,
		`INSERT INTO feeding_logs (id, pond_id, speed, duration, decision_json, created_at)
		 VALUES ($1, $2, $3, $4, $5, $6)`,
		fl.ID, fl.PondID, fl.Speed, fl.Duration, fl.DecisionJSON, fl.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("store: insert feeding log: %w", err)
	}
	return nil
}

// Close releases the connection pool.
func (p *PostgresStore) Close() {
	if p.pool != nil {
		p.pool.Close()
	}
}

// Farm represents a registered aquaculture farm.
type Farm struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Location  string    `json:"location"`
	AreaM2    float64   `json:"area_m2"`
	Species   string    `json:"species"`
	CreatedAt time.Time `json:"created_at"`
}

// Pond represents a pond within a farm.
type Pond struct {
	ID        string    `json:"id"`
	FarmID    string    `json:"farm_id"`
	Name      string    `json:"name"`
	AreaM2    float64   `json:"area_m2"`
	DepthM    float64   `json:"depth_m"`
	FishCount int       `json:"fish_count"`
	CreatedAt time.Time `json:"created_at"`
}

// Alert records a water-quality or device alert.
type Alert struct {
	ID         string     `json:"id"`
	FarmID     string     `json:"farm_id"`
	PondID     *string    `json:"pond_id,omitempty"`
	Level      string     `json:"level"`
	Type       string     `json:"type"`
	Message    string     `json:"message"`
	Status     string     `json:"status"`
	CreatedAt  time.Time  `json:"created_at"`
	ResolvedAt *time.Time `json:"resolved_at,omitempty"`
}
