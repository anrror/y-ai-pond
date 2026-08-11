package server

import (
	"context"
	"errors"
	"log/slog"

	"github.com/anrror/y-ai-pond/internal/handler"
	"github.com/anrror/y-ai-pond/pkg/store"
	"github.com/gin-gonic/gin"
)

// HandlerModule owns the REST API handler. It registers the /api/v1 routes on
// the server router during Init; it has no background work.
type HandlerModule struct {
	log    *slog.Logger
	h      *handler.Handler
	router *gin.Engine
}

// NewHandlerModule creates a handler module bound to the store and auth modules.
func NewHandlerModule(log *slog.Logger, sm *StoreModule, am *AuthModule) *HandlerModule {
	if log == nil {
		log = slog.Default()
	}
	var influx store.InfluxWriter = sm.Influx()
	var pg = sm.Postgres().Pool()
	return &HandlerModule{
		log: log,
		h:   handler.NewHandler(pg, influx, am.Service(), log),
	}
}

// ID returns the module identifier.
func (m *HandlerModule) ID() string { return "handler" }

// SetRouter wires the Gin engine into the module. The router is created by
// server.New, so SetRouter must be called after New and before Run.
func (m *HandlerModule) SetRouter(r *gin.Engine) { m.router = r }

// Init registers the REST API routes on the server router.
func (m *HandlerModule) Init(ctx context.Context) error {
	if m.router == nil {
		return errors.New("handler: router not set; call SetRouter after server.New")
	}
	m.h.RegisterRoutes(m.router)
	m.log.Info("handler: REST API routes registered")
	return nil
}

// Start is a no-op; routes are static once registered.
func (m *HandlerModule) Start(ctx context.Context) error { return nil }

// Stop is a no-op.
func (m *HandlerModule) Stop(ctx context.Context) error { return nil }
