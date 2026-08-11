package server

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/anrror/y-ai-pond/internal/config"
	"github.com/anrror/y-ai-pond/internal/mqtt"
	mqttclient "github.com/anrror/y-ai-pond/pkg/mqtt"
	"github.com/anrror/y-ai-pond/pkg/store"
)

// GatewayModule owns the cloud MQTT Gateway: it creates the autopaho client,
// wires the protobuf-ingesting handler, subscribes to topic patterns, and
// connects asynchronously so a down broker never blocks server startup
// (per QA: "MQTT 未连接 → 启动警告(非致命)"; autopaho retries with backoff).
type GatewayModule struct {
	cfg    *config.Config
	log    *slog.Logger
	gw     *mqtt.Gateway
	client *mqttclient.Client
}

// NewGatewayModule creates a gateway module bound to the given stores.
func NewGatewayModule(cfg *config.Config, log *slog.Logger, sm *StoreModule) *GatewayModule {
	if log == nil {
		log = slog.Default()
	}
	var influx store.InfluxWriter = sm.Influx()
	var pg = sm.Postgres().Pool()

	gw := mqtt.NewGateway(nil, influx, pg, log)
	client := mqttclient.New(mqttclient.Config{
		BrokerURL:     cfg.MQTT.BrokerURL,
		ClientID:      cfg.MQTT.ClientID,
		KeepAlive:     uint16(cfg.MQTT.Keepalive),     //nolint:gosec // G115: bounds guaranteed by config validation
		SessionExpiry: uint32(cfg.MQTT.SessionExpiry), //nolint:gosec // G115: bounds guaranteed by config validation
	}, gw.HandleMessage, log)
	gw.SetClient(client)

	return &GatewayModule{cfg: cfg, log: log, gw: gw, client: client}
}

// ID returns the module identifier.
func (m *GatewayModule) ID() string { return "gateway" }

// Init subscribes to the gateway topic patterns. Subscriptions are queued in
// the client and re-applied on connection, so this never blocks on a dead broker.
func (m *GatewayModule) Init(ctx context.Context) error {
	return m.gw.Start(ctx)
}

// Start launches the MQTT connection in the background. Connect returns after
// the first successful CONNACK or when ctx is cancelled; autopaho reconnects
// with exponential backoff while ctx is alive.
func (m *GatewayModule) Start(ctx context.Context) error {
	m.log.Info("gateway: connecting to broker in background",
		"broker_url", m.cfg.MQTT.BrokerURL,
		"client_id", m.cfg.MQTT.ClientID)
	go func() {
		if err := m.client.Connect(ctx); err != nil {
			if ctx.Err() != nil {
				return // shutting down
			}
			m.log.Warn("gateway: MQTT connect failed (broker may be down); retrying in background",
				"error", err)
		} else {
			m.log.Info("gateway: MQTT connected", "broker_url", m.cfg.MQTT.BrokerURL)
		}
	}()
	return nil
}

// Stop drains MQTT before closing.
func (m *GatewayModule) Stop(ctx context.Context) error {
	disCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := m.client.Disconnect(disCtx); err != nil {
		m.log.Warn("gateway: disconnect error", "error", err)
	}
	m.log.Info("gateway: MQTT disconnected")
	return nil
}

// HealthChecks returns the module's health check functions.
func (m *GatewayModule) HealthChecks() map[string]HealthCheckFunc {
	return map[string]HealthCheckFunc{
		"mqtt": func(ctx context.Context) error {
			ctx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
			defer cancel()
			if err := m.client.AwaitConnected(ctx); err != nil {
				return errors.New("MQTT broker not connected")
			}
			return nil
		},
	}
}
