// Package main is the entry point for the y-ai-pond cloud (TIER 2) HTTP server.
//
// Startup follows the y-ai-agent-base pattern: load config -> create components
// (store, auth, MQTT gateway) -> build modules -> server.Run() with graceful
// shutdown (drain MQTT -> close DB pools -> exit).
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"

	"github.com/anrror/y-ai-pond/internal/config"
	"github.com/anrror/y-ai-pond/internal/server"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "fatal: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	configPath := flag.String("config", "", "path to config YAML file (env: POND_CONFIG)")
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	logger.Info("y-ai-pond cloud server",
		"port", cfg.Server.Port,
		"sse_timeout", cfg.Server.SSETimeout,
		"mqtt_broker", cfg.MQTT.BrokerURL,
		"db_redis", cfg.Database.RedisAddr,
		"model_registry", cfg.Models.RegistryDir,
	)

	// Store module fast-fails if PostgreSQL is unreachable (QA: "DB 未连接 → 启动报错").
	storeMod, err := server.NewStoreModule(cfg, logger)
	if err != nil {
		return fmt.Errorf("store: %w", err)
	}

	authMod := server.NewAuthModule(cfg, logger)
	handlerMod := server.NewHandlerModule(logger, storeMod, authMod)

	srv, err := server.New(cfg, logger,
		server.WithModule(storeMod),
		server.WithModule(authMod),
		server.WithModule(server.NewGatewayModule(cfg, logger, storeMod)),
		server.WithModule(handlerMod),
	)
	if err != nil {
		return fmt.Errorf("server: %w", err)
	}

	// The router is created by server.New; wire it into the handler module so
	// routes register during module Init inside Run.
	handlerMod.SetRouter(srv.Router())

	return srv.Run(context.Background())
}
