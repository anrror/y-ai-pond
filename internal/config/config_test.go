package config

import (
	"os"
	"path/filepath"
	"testing"
)

// writeTempConfig writes a YAML string to a temporary file and returns its path.
func writeTempConfig(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write temp config: %v", err)
	}
	return path
}

func TestLoad_expandsEnvVars(t *testing.T) {
	path := writeTempConfig(t, `
server:
  port: 8080
auth:
  jwt_secret: "$(JWT_SECRET)"
  token_ttl: 86400
database:
  postgres_dsn: "postgres://pond:$(POSTGRES_PASSWORD)@host:5432/db?sslmode=disable"
  redis_addr: "redis:6379"
  influxdb:
    url: "http://influxdb:8086"
    token: "$(INFLUXDB_TOKEN)"
    org: "y-ai-pond"
`)

	t.Setenv("JWT_SECRET", "my-secret-key")
	t.Setenv("POSTGRES_PASSWORD", "my-db-password")
	t.Setenv("INFLUXDB_TOKEN", "my-influx-token")

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.Auth.JWTSecret != "my-secret-key" {
		t.Errorf("JWTSecret = %q, want %q", cfg.Auth.JWTSecret, "my-secret-key")
	}
	if cfg.Database.PostgresDSN != "postgres://pond:my-db-password@host:5432/db?sslmode=disable" {
		t.Errorf("PostgresDSN = %q, want expanded password", cfg.Database.PostgresDSN)
	}
	if cfg.Database.InfluxDB.Token != "my-influx-token" {
		t.Errorf("InfluxDB.Token = %q, want %q", cfg.Database.InfluxDB.Token, "my-influx-token")
	}
	// Plain value without placeholder must be unchanged
	if cfg.Database.RedisAddr != "redis:6379" {
		t.Errorf("RedisAddr = %q, want %q", cfg.Database.RedisAddr, "redis:6379")
	}
	if cfg.Server.Port != 8080 {
		t.Errorf("Server.Port = %d, want 8080", cfg.Server.Port)
	}
}

func TestLoad_unsetEnvVar_keepsPlaceholder(t *testing.T) {
	path := writeTempConfig(t, `
auth:
  jwt_secret: "$(JWT_SECRET)"
  token_ttl: 3600
`)

	// intentionally NOT setting JWT_SECRET

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	// Placeholder should be kept as-is
	if cfg.Auth.JWTSecret != "$(JWT_SECRET)" {
		t.Errorf("JWTSecret = %q, want placeholder %q", cfg.Auth.JWTSecret, "$(JWT_SECRET)")
	}
}

func TestLoad_noPlaceholders_unchanged(t *testing.T) {
	path := writeTempConfig(t, `
server:
  port: 9090
mqtt:
  broker_url: "tcp://emqx:1883"
  client_id: "test-client"
database:
  redis_addr: "localhost:6379"
`)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.Server.Port != 9090 {
		t.Errorf("Server.Port = %d, want 9090", cfg.Server.Port)
	}
	if cfg.MQTT.BrokerURL != "tcp://emqx:1883" {
		t.Errorf("BrokerURL = %q, want %q", cfg.MQTT.BrokerURL, "tcp://emqx:1883")
	}
	if cfg.Database.RedisAddr != "localhost:6379" {
		t.Errorf("RedisAddr = %q, want %q", cfg.Database.RedisAddr, "localhost:6379")
	}
}

func TestLoad_nestedExpansion(t *testing.T) {
	// Tests expansion in nested structs (InfluxDB config inside Database)
	path := writeTempConfig(t, `
database:
  influxdb:
    url: "http://host:8086"
    token: "$(INFLUX_TOKEN)"
    org: $(INFLUX_ORG)
`)

	t.Setenv("INFLUX_TOKEN", "tok-123")
	t.Setenv("INFLUX_ORG", "my-org")

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.Database.InfluxDB.Token != "tok-123" {
		t.Errorf("Token = %q, want %q", cfg.Database.InfluxDB.Token, "tok-123")
	}
	if cfg.Database.InfluxDB.Org != "my-org" {
		t.Errorf("Org = %q, want %q", cfg.Database.InfluxDB.Org, "my-org")
	}
}

func TestLoad_partialExpansion(t *testing.T) {
	// DSN has both a placeholder and a literal part
	path := writeTempConfig(t, `
database:
  postgres_dsn: "postgres://user:$(PG_PASS)@localhost:5432/mydb?sslmode=disable"
`)

	t.Setenv("PG_PASS", "secret123")

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	want := "postgres://user:secret123@localhost:5432/mydb?sslmode=disable"
	if cfg.Database.PostgresDSN != want {
		t.Errorf("PostgresDSN = %q, want %q", cfg.Database.PostgresDSN, want)
	}
}

func TestLoad_multiplePlaceholdersInOneValue(t *testing.T) {
	path := writeTempConfig(t, `
mqtt:
  broker_url: "$(PROTO)://$(HOST):$(PORT)"
`)

	t.Setenv("PROTO", "tcp")
	t.Setenv("HOST", "broker.local")
	t.Setenv("PORT", "1883")

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	want := "tcp://broker.local:1883"
	if cfg.MQTT.BrokerURL != want {
		t.Errorf("BrokerURL = %q, want %q", cfg.MQTT.BrokerURL, want)
	}
}

func TestLoad_fileNotFound(t *testing.T) {
	_, err := Load("/nonexistent/config.yaml")
	if err == nil {
		t.Fatal("expected error for non-existent file")
	}
}

func TestLoad_invalidYAML(t *testing.T) {
	path := writeTempConfig(t, `this: } is: not: valid: yaml [[[`)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for invalid YAML")
	}
}

func TestExpandEnvVars_mixedTypes(t *testing.T) {
	input := map[string]interface{}{
		"str":    "$(FOO)",
		"int":    42,
		"float":  3.14,
		"bool":   true,
		"nested": map[string]interface{}{"key": "$(BAR)"},
		"list":   []interface{}{"$(BAZ)", 99},
	}

	t.Setenv("FOO", "foo_val")
	t.Setenv("BAR", "bar_val")
	t.Setenv("BAZ", "baz_val")

	result, ok := expandEnvVars(input).(map[string]interface{})
	if !ok {
		t.Fatal("expandEnvVars returned unexpected type")
	}

	if result["str"] != "foo_val" {
		t.Errorf("str = %q, want foo_val", result["str"])
	}
	if result["int"] != 42 {
		t.Errorf("int = %v, want 42", result["int"])
	}
	if result["float"] != 3.14 {
		t.Errorf("float = %v, want 3.14", result["float"])
	}
	if result["bool"] != true {
		t.Errorf("bool = %v, want true", result["bool"])
	}
	nested, ok := result["nested"].(map[string]interface{})
	if !ok {
		t.Fatal("nested is not map[string]interface{}")
	}
	if nested["key"] != "bar_val" {
		t.Errorf("nested.key = %q, want bar_val", nested["key"])
	}
	list, ok := result["list"].([]interface{})
	if !ok {
		t.Fatal("list is not []interface{}")
	}
	if list[0] != "baz_val" {
		t.Errorf("list[0] = %q, want baz_val", list[0])
	}
	if list[1] != 99 {
		t.Errorf("list[1] = %v, want 99", list[1])
	}
}
