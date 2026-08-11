// Package main is the entry point for the y-ai-pond edge (TIER 1) controller.
package main

import (
	"flag"
	"fmt"
	"log/slog"
	"os"

	"github.com/anrror/y-ai-pond/internal/config"
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

	slog.Info("y-ai-pond edge controller",
		"mqtt_broker", cfg.MQTT.BrokerURL,
		"mqtt_keepalive", cfg.MQTT.Keepalive,
		"sensor_interval_ph", cfg.Edge.SensorIntervals["ph"],
		"sensor_interval_do", cfg.Edge.SensorIntervals["do"],
	)

	slog.Info("edge stub — full controller logic (T8+) pending")
	return nil
}
