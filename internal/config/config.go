// Package config loads y-ai-pond configuration from a YAML file via Viper.
package config

import (
	"fmt"
	"log"
	"os"
	"regexp"

	"github.com/spf13/viper"
)

// Config is the root configuration for y-ai-pond.
type Config struct {
	Server   ServerConfig   `mapstructure:"server"`
	MQTT     MQTTConfig     `mapstructure:"mqtt"`
	Database DatabaseConfig `mapstructure:"database"`
	Models   ModelsConfig   `mapstructure:"models"`
	Edge     EdgeConfig     `mapstructure:"edge"`
	Auth     AuthConfig     `mapstructure:"auth"`
}

// AuthConfig holds JWT signing parameters.
type AuthConfig struct {
	JWTSecret string `mapstructure:"jwt_secret"`
	TokenTTL  int    `mapstructure:"token_ttl"` // seconds; 0 -> 24h
}

// ServerConfig holds the HTTP/SSE server settings.
type ServerConfig struct {
	Port       int `mapstructure:"port"`
	SSETimeout int `mapstructure:"sse_timeout"`
}

// MQTTConfig holds MQTT broker connection parameters.
type MQTTConfig struct {
	BrokerURL     string `mapstructure:"broker_url"`
	ClientID      string `mapstructure:"client_id"`
	Keepalive     int    `mapstructure:"keepalive"`
	SessionExpiry int    `mapstructure:"session_expiry"`
}

// InfluxDBConfig holds InfluxDB connection parameters.
type InfluxDBConfig struct {
	URL   string `mapstructure:"url"`
	Token string `mapstructure:"token"`
	Org   string `mapstructure:"org"`
}

// DatabaseConfig holds database connection strings.
type DatabaseConfig struct {
	PostgresDSN string         `mapstructure:"postgres_dsn"`
	InfluxDB    InfluxDBConfig `mapstructure:"influxdb"`
	RedisAddr   string         `mapstructure:"redis_addr"`
}

// ModelsConfig holds model registry paths.
type ModelsConfig struct {
	RegistryDir string            `mapstructure:"registry_dir"`
	Paths       map[string]string `mapstructure:"paths"`
}

// EdgeConfig holds edge-specific settings.
type EdgeConfig struct {
	SensorIntervals map[string]int `mapstructure:"sensor_intervals"`
}

// envVarPattern matches $(VAR_NAME) placeholders injected by the K8s ConfigMap.
var envVarPattern = regexp.MustCompile(`\$\(([A-Z_]+)\)`)

// Load reads configuration from the given YAML file path and expands $(VAR)
// placeholders using os.Getenv. If path is empty, it checks POND_CONFIG env
// var, then falls back to "config/config.yaml".
func Load(path string) (*Config, error) {
	if path == "" {
		path = os.Getenv("POND_CONFIG")
	}
	if path == "" {
		path = "config/config.yaml"
	}

	v := viper.New()
	v.SetConfigFile(path)
	v.SetConfigType("yaml")

	if err := v.ReadInConfig(); err != nil {
		return nil, fmt.Errorf("config: read file %q: %w", path, err)
	}

	// Expand $(VAR) placeholders in all config values before unmarshalling.
	// K8s ConfigMap injects $(POSTGRES_PASSWORD), $(JWT_SECRET), etc. which
	// are resolved from environment variables (set via Secret keyRef).
	settings := v.AllSettings()
	expanded, ok := expandEnvVars(settings).(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("config: expandEnvVars did not return map")
	}

	v2 := viper.New()
	if err := v2.MergeConfigMap(expanded); err != nil {
		return nil, fmt.Errorf("config: merge expanded settings: %w", err)
	}

	var cfg Config
	if err := v2.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("config: unmarshal: %w", err)
	}

	return &cfg, nil
}

// expandEnvVars recursively walks a map/slice and replaces $(VAR) with
// os.Getenv(VAR) in string values. Unset environment variables are left as-is
// and a warning is logged.
func expandEnvVars(val interface{}) interface{} {
	switch v := val.(type) {
	case map[string]interface{}:
		out := make(map[string]interface{}, len(v))
		for k, vv := range v {
			out[k] = expandEnvVars(vv)
		}
		return out
	case []interface{}:
		out := make([]interface{}, len(v))
		for i, vv := range v {
			out[i] = expandEnvVars(vv)
		}
		return out
	case string:
		return expandEnvString(v)
	default:
		return v
	}
}

// expandEnvString replaces all $(VAR) placeholders in s with the
// corresponding environment variable values.
func expandEnvString(s string) string {
	return envVarPattern.ReplaceAllStringFunc(s, func(match string) string {
		// match is e.g. "$(POSTGRES_PASSWORD)"
		varName := match[2 : len(match)-1] // strip "$(" and ")"
		if val, ok := os.LookupEnv(varName); ok {
			return val
		}
		log.Printf("config: environment variable %q not set, keeping placeholder", varName)
		return match
	})
}
