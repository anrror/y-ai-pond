package server

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/anrror/y-ai-pond/internal/config"
	"github.com/anrror/y-ai-pond/pkg/store"
)

// StoreModule owns the database layer: PostgreSQL (business), Redis (cache),
// InfluxDB (time-series). It registers health checks and closes pools on Stop.
type StoreModule struct {
	cfg      *config.Config
	log      *slog.Logger
	postgres *store.PostgresStore
	redis    *store.RedisStore
	influx   *store.InfluxStore
}

// NewStoreModule creates a store module. Fast fail: if PostgreSQL is
// unreachable the module reports an error so server startup aborts (per QA:
// "DB 未连接 → 启动报错").
func NewStoreModule(cfg *config.Config, log *slog.Logger) (*StoreModule, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	pg, err := store.NewPostgres(ctx, cfg.Database.PostgresDSN)
	if err != nil {
		return nil, err
	}

	redis, err := store.NewRedis(cfg.Database.RedisAddr)
	if err != nil {
		pg.Close()
		return nil, err
	}

	influx, err := store.NewInflux(store.InfluxConfig{
		URL:   cfg.Database.InfluxDB.URL,
		Token: cfg.Database.InfluxDB.Token,
		Org:   cfg.Database.InfluxDB.Org,
	})
	if err != nil {
		pg.Close()
		return nil, err
	}

	return &StoreModule{
		cfg:      cfg,
		log:      log,
		postgres: pg,
		redis:    redis,
		influx:   influx,
	}, nil
}

// ID returns the module identifier.
func (m *StoreModule) ID() string { return "store" }

// Init registers health checks for all three stores.
func (m *StoreModule) Init(ctx context.Context) error {
	return nil
}

// Start registers component health probes on the server. The server registers
// them during its own setup; here we surface pings.
func (m *StoreModule) Start(ctx context.Context) error {
	m.log.Info("store: postgres, redis, influx ready")
	return nil
}

// Stop closes database pools in reverse dependency order.
func (m *StoreModule) Stop(ctx context.Context) error {
	if m.influx != nil {
		_ = m.influx.Close()
	}
	if m.redis != nil {
		_ = m.redis.Close()
	}
	if m.postgres != nil {
		m.postgres.Close()
	}
	m.log.Info("store: database pools closed")
	return nil
}

// Postgres returns the PostgreSQL store.
func (m *StoreModule) Postgres() *store.PostgresStore { return m.postgres }

// Redis returns the Redis store.
func (m *StoreModule) Redis() *store.RedisStore { return m.redis }

// Influx returns the InfluxDB store.
func (m *StoreModule) Influx() *store.InfluxStore { return m.influx }

// HealthChecks returns the module's health check functions keyed by name,
// for wiring into the server's /health handler.
func (m *StoreModule) HealthChecks() map[string]HealthCheckFunc {
	return map[string]HealthCheckFunc{
		"postgres": func(ctx context.Context) error {
			if err := m.postgres.Ping(ctx); err != nil {
				return err
			}
			return nil
		},
		"redis": func(ctx context.Context) error {
			if err := m.redis.Ping(); err != nil {
				return err
			}
			return nil
		},
		"influx": func(ctx context.Context) error {
			if m.influx == nil {
				return errors.New("influx store not initialized")
			}
			return nil
		},
	}
}
