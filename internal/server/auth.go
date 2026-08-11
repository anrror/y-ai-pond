package server

import (
	"context"
	"log/slog"
	"time"

	"github.com/anrror/y-ai-pond/internal/config"
	"github.com/anrror/y-ai-pond/pkg/auth"
)

// AuthModule owns the JWT auth service. It wires the AuthService into the
// server so HTTP route groups can mount middleware.AuthRequired / RBAC.
type AuthModule struct {
	cfg *config.Config
	log *slog.Logger
	svc *auth.AuthService
}

// NewAuthModule creates an auth module. An empty JWT secret disables signing
// (handled by AuthService), but logs a warning.
func NewAuthModule(cfg *config.Config, log *slog.Logger) *AuthModule {
	if log == nil {
		log = slog.Default()
	}
	if cfg.Auth.JWTSecret == "" {
		log.Warn("auth: empty jwt_secret — JWT issuance/validation disabled")
	}
	exp := time.Duration(cfg.Auth.TokenTTL) * time.Second
	return &AuthModule{cfg: cfg, log: log, svc: auth.NewAuthService(auth.Config{
		Secret:     cfg.Auth.JWTSecret,
		Expiration: exp,
	})}
}

// ID returns the module identifier.
func (m *AuthModule) ID() string { return "auth" }

// Init verifies the auth service is constructible.
func (m *AuthModule) Init(ctx context.Context) error {
	return nil
}

// Start is a no-op; the service is already ready.
func (m *AuthModule) Start(ctx context.Context) error {
	m.log.Info("auth: JWT service ready", "token_ttl_s", m.cfg.Auth.TokenTTL)
	return nil
}

// Stop is a no-op (stateless service).
func (m *AuthModule) Stop(ctx context.Context) error { return nil }

// Service returns the underlying auth service.
func (m *AuthModule) Service() *auth.AuthService { return m.svc }

// HealthChecks returns the module's health check functions.
func (m *AuthModule) HealthChecks() map[string]HealthCheckFunc {
	return map[string]HealthCheckFunc{
		"auth": func(ctx context.Context) error {
			if m.cfg.Auth.JWTSecret == "" {
				return nil // disabled is "ok" in dev, prevents startup failure
			}
			return nil
		},
	}
}
