// Package server builds and runs the y-ai-pond cloud HTTP server following the
// y-ai-agent-base module lifecycle pattern (Init/Start/Stop) adapted for Gin.
//
// Lifecycle:
//
//	srv, err := server.New(cfg, log,
//	    server.WithModule(storeModule),
//	    server.WithModule(authModule),
//	    server.WithModule(gatewayModule),
//	)
//	srv.Run(ctx) // Init -> Start -> Serve -> (shutdown) -> Stop (reverse order)
package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/anrror/y-ai-pond/internal/config"
)

// Module is the y-ai-pond plugin lifecycle interface (mirrors the
// y-ai-agent-base Module contract, adapted to a Gin HTTP server).
type Module interface {
	// ID returns a unique module identifier.
	ID() string
	// Init performs one-time setup. Registered modules run Init in order.
	Init(ctx context.Context) error
	// Start launches background goroutines. Returns after starting, or on error.
	// The context is cancelled when the server shuts down.
	Start(ctx context.Context) error
	// Stop shuts the module down gracefully. Called in reverse registration order.
	Stop(ctx context.Context) error
}

// HealthCheckFunc performs a synchronous component health check.
type HealthCheckFunc func(ctx context.Context) error

// Server runs the HTTP server with module lifecycle management.
type Server struct {
	cfg     *config.Config
	log     *slog.Logger
	modules []Module
	router  *gin.Engine

	healthMu sync.Mutex
	health   map[string]HealthCheckFunc

	hs     *http.Server
	start  time.Time
	cancel context.CancelFunc
}

// New creates a Server with the given config and option functions.
func New(cfg *config.Config, log *slog.Logger, opts ...Option) (*Server, error) {
	if cfg == nil {
		return nil, errors.New("server: nil config")
	}
	if log == nil {
		log = slog.Default()
	}

	s := &Server{
		cfg:    cfg,
		log:    log,
		health: make(map[string]HealthCheckFunc),
		start:  time.Now(),
	}

	for _, opt := range opts {
		if err := opt(s); err != nil {
			return nil, err
		}
	}

	gin.SetMode(gin.ReleaseMode)
	s.router = gin.New()
	s.router.Use(gin.Recovery())

	return s, nil
}

// Option configures a Server.
type Option func(*Server) error

// WithModule adds a lifecycle module to the server.
func WithModule(m Module) Option {
	return func(s *Server) error {
		s.modules = append(s.modules, m)
		return nil
	}
}

// RegisterHealthCheck registers a named health check function for /health.
func (s *Server) RegisterHealthCheck(name string, fn HealthCheckFunc) {
	s.healthMu.Lock()
	defer s.healthMu.Unlock()
	s.health[name] = fn
}

// Server returns the component set; only core health probes are served here.
// Business API routes mount via modules on the router.

// Router returns the Gin engine for route registration.
func (s *Server) Router() *gin.Engine { return s.router }

// Run starts the server and blocks until ctx is cancelled or a fatal signal
// arrives (SIGINT/SIGTERM), then shuts down gracefully.
//
// Module lifecycle: Init (registration order) -> Start (registration order) ->
// HTTP serve -> Stop (reverse order). Stop runs after in-flight HTTP requests
// drain, matching the "drain MQTT -> close DB pools -> exit" shutdown order.
func (s *Server) Run(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithCancel(ctx)
	s.cancel = cancel

	if err := s.initModules(ctx); err != nil {
		return err
	}
	if err := s.startModules(ctx); err != nil {
		return err
	}

	s.setupHTTPServer()

	// Block until caller cancellation or a termination signal.
	done := make(chan error, 1)
	go func() {
		done <- s.serve(ctx)
	}()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	select {
	case err := <-done:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return err
		}
	case <-sig:
		s.log.Info("server: shutdown signal received")
	case <-ctx.Done():
		s.log.Info("server: context cancelled")
	}

	return s.shutdown(context.Background())
}

// initModules runs each module's Init in registration order. Modules that
// expose HealthChecks() have their probes registered for /health.
func (s *Server) initModules(ctx context.Context) error {
	for _, m := range s.modules {
		s.log.Info("server: initializing module", "module", m.ID())
		if err := m.Init(ctx); err != nil {
			return fmt.Errorf("server: module %q init: %w", m.ID(), err)
		}
		if hc, ok := m.(interface {
			HealthChecks() map[string]HealthCheckFunc
		}); ok {
			for name, fn := range hc.HealthChecks() {
				s.RegisterHealthCheck(name, fn)
			}
		}
	}
	return nil
}

// startModules runs each module's Start in registration order.
func (s *Server) startModules(ctx context.Context) error {
	for _, m := range s.modules {
		s.log.Info("server: starting module", "module", m.ID())
		if err := m.Start(ctx); err != nil {
			return fmt.Errorf("server: module %q start: %w", m.ID(), err)
		}
	}
	return nil
}

// setupHTTPServer wires health, metrics, and the Router. Modules registered
// routes on s.router during Init, so the engine is fully assembled here.
func (s *Server) setupHTTPServer() {
	s.router.GET("/health", s.handleHealth)
	s.router.GET("/metrics", s.handleMetrics)

	// SSE connections live indefinitely: cfg.Server.SSETimeout == 0 means "no
	// timeout". Using 0 for WriteTimeout keeps SSE streams from being cut by
	// the default http.Server write deadline.
	port := 8080
	if s.cfg != nil {
		port = s.cfg.Server.Port
		if port == 0 {
			port = 8080
		}
	}
	timeout := 0
	if s.cfg != nil {
		timeout = s.cfg.Server.SSETimeout
	}
	s.hs = &http.Server{
		Addr:         fmt.Sprintf(":%d", port),
		Handler:      s.router,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: time.Duration(timeout) * time.Second,
		IdleTimeout:  120 * time.Second,
	}
}

// serve listens on the configured port. Returns when the listener fails or is closed.
func (s *Server) serve(_ context.Context) error {
	s.log.Info("server: HTTP server listening",
		"addr", s.hs.Addr,
		"sse_timeout_s", func() int {
			if s.cfg != nil {
				return s.cfg.Server.SSETimeout
			}
			return 0
		}(),
		"modules", len(s.modules),
	)
	if err := s.hs.ListenAndServe(); err != nil {
		return err
	}
	return nil
}

// shutdown triggers HTTP drain, then stops modules in reverse order.
func (s *Server) shutdown(ctx context.Context) error {
	s.log.Info("server: shutting down HTTP server")
	shutCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if s.hs != nil {
		if err := s.hs.Shutdown(shutCtx); err != nil {
			s.log.Warn("server: http shutdown error", "error", err)
		}
	}

	s.log.Info("server: stopping modules", "count", len(s.modules))
	for i := len(s.modules) - 1; i >= 0; i-- {
		m := s.modules[i]
		if err := m.Stop(shutCtx); err != nil {
			s.log.Error("server: module stop error", "module", m.ID(), "error", err)
		} else {
			s.log.Info("server: module stopped", "module", m.ID())
		}
	}

	if s.cancel != nil {
		s.cancel()
	}
	s.log.Info("server: shutdown complete")
	return nil
}
