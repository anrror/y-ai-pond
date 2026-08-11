package store

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/pashagolub/pgxmock/v4"
)

// ---------------------------------------------------------------------------
// InfluxDB — fake-based test
// ---------------------------------------------------------------------------

// fakeInfluxWriter implements InfluxWriter for testing.
type fakeInfluxWriter struct {
	written []SensorPoint
}

func (f *fakeInfluxWriter) WriteSensorData(_ context.Context, points []SensorPoint) error {
	f.written = append(f.written, points...)
	return nil
}

func (f *fakeInfluxWriter) QueryTimeRange(_ context.Context, _ string, _ string, _ string) ([]Point, error) {
	return []Point{}, nil
}

func (f *fakeInfluxWriter) Close() error { return nil }

// Given a valid InfluxConfig, NewInflux returns an InfluxStore without error.
func TestNewInflux_when_validConfig(t *testing.T) {
	s, err := NewInflux(InfluxConfig{
		URL:   "http://localhost:8086",
		Token: "test-token",
		Org:   "y-ai-pond",
	})
	if err != nil {
		t.Fatalf("NewInflux failed: %v", err)
	}
	if s == nil {
		t.Fatal("expected non-nil InfluxStore")
	}
}

// Given an empty URL, NewInflux returns an error.
func TestNewInflux_when_emptyURL(t *testing.T) {
	_, err := NewInflux(InfluxConfig{URL: ""})
	if err == nil {
		t.Fatal("expected error for empty URL")
	}
}

// Given a fake InfluxWriter, WriteSensorData stores points.
func TestInfluxWrite_when_fake_writer(t *testing.T) {
	ctx := context.Background()
	fake := &fakeInfluxWriter{}
	points := []SensorPoint{
		{
			FarmID:     "farm-1",
			PondID:     "pond-1",
			SensorType: "ph",
			Timestamp:  time.Now(),
			Fields:     map[string]float64{"ph": 7.2},
		},
	}
	if err := fake.WriteSensorData(ctx, points); err != nil {
		t.Fatalf("WriteSensorData failed: %v", err)
	}
	if len(fake.written) != 1 {
		t.Fatalf("expected 1 point written, got %d", len(fake.written))
	}
}

// ---------------------------------------------------------------------------
// PostgreSQL — pgxmock-based test
// ---------------------------------------------------------------------------

// Given a mock pool that expects Ping, PostgresStore.Ping succeeds.
func TestPostgresPing_when_mock_succeeds(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatalf("failed to create mock pool: %v", err)
	}
	defer mock.Close()

	mock.ExpectPing()

	store := NewPostgresWithPool(mock)
	ctx := context.Background()
	if err := store.Ping(ctx); err != nil {
		t.Fatalf("Ping failed: %v", err)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

// Given a mock pool that returns error on Ping, PostgresStore.Ping returns error.
func TestPostgresPing_when_mock_fails(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatalf("failed to create mock pool: %v", err)
	}
	defer mock.Close()

	mock.ExpectPing().WillReturnError(ErrNotOpen)

	store := NewPostgresWithPool(mock)
	ctx := context.Background()
	if err := store.Ping(ctx); err == nil {
		t.Fatal("expected Ping to fail")
	}
}

// Given a nil pool, PostgresStore.Ping returns ErrNotOpen.
func TestPostgresPing_when_nil_pool(t *testing.T) {
	store := &PostgresStore{pool: nil}
	ctx := context.Background()
	if err := store.Ping(ctx); err == nil {
		t.Fatal("expected ErrNotOpen")
	}
}

// Given a mock pool that expects UpsertDevice, the operation succeeds.
func TestPostgresUpsertDevice_when_mock_succeeds(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatalf("failed to create mock pool: %v", err)
	}
	defer mock.Close()

	device := Device{
		ID:              "dev-1",
		FarmID:          "farm-1",
		PondID:          "pond-1",
		Type:            "sensor",
		Status:          "online",
		FirmwareVersion: "v1.0",
		LastHeartbeat:   time.Now(),
	}

	mock.ExpectExec("INSERT INTO devices").
		WithArgs(device.ID, device.FarmID, device.PondID, device.Type, device.Status, device.FirmwareVersion, device.LastHeartbeat).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))

	store := NewPostgresWithPool(mock)
	ctx := context.Background()
	if err := store.UpsertDevice(ctx, device); err != nil {
		t.Fatalf("UpsertDevice failed: %v", err)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Redis — miniredis-based test
// ---------------------------------------------------------------------------

// Given a miniredis server, Ping succeeds.
func TestRedisPing_when_miniredis(t *testing.T) {
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("failed to start miniredis: %v", err)
	}
	defer mr.Close()

	store, err := NewRedis(mr.Addr())
	if err != nil {
		t.Fatalf("NewRedis failed: %v", err)
	}
	defer func() { _ = store.Close() }()

	if err := store.Ping(); err != nil {
		t.Fatalf("Ping failed: %v", err)
	}
}

// Given a miniredis server, SetShadow stores and GetShadow retrieves.
func TestRedisSetShadow_and_GetShadow(t *testing.T) {
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("failed to start miniredis: %v", err)
	}
	defer mr.Close()

	store, err := NewRedis(mr.Addr())
	if err != nil {
		t.Fatalf("NewRedis failed: %v", err)
	}

	deviceID := "dev-shadow-1"
	shadowJSON := `{"status":"online","temp":25.5}`
	
	setErr := store.SetShadow(deviceID, shadowJSON)
	if setErr != nil {
		t.Fatalf("SetShadow failed: %v", setErr)
	}

	got, err := store.GetShadow(deviceID)
	if err != nil {
		t.Fatalf("GetShadow failed: %v", err)
	}
	if got != shadowJSON {
		t.Fatalf("expected %q, got %q", shadowJSON, got)
	}
}

// Given a non-existent key, GetShadow returns ErrNotFound.
func TestRedisGetShadow_when_notFound(t *testing.T) {
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("failed to start miniredis: %v", err)
	}
	defer mr.Close()

	store, err := NewRedis(mr.Addr())
	if err != nil {
		t.Fatalf("NewRedis failed: %v", err)
	}

	_, err = store.GetShadow("nonexistent")
	if err == nil {
		t.Fatal("expected error for missing key")
	}
}

// Given a miniredis server, SetNX works for deduplication.
func TestRedisSetNX_when_key_not_exists(t *testing.T) {
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("failed to start miniredis: %v", err)
	}
	defer mr.Close()

	store, err := NewRedis(mr.Addr())
	if err != nil {
		t.Fatalf("NewRedis failed: %v", err)
	}

	ok, err := store.SetNX("alert:dedup:1", 60)
	if err != nil {
		t.Fatalf("SetNX failed: %v", err)
	}
	if !ok {
		t.Fatal("expected SetNX to succeed on first call")
	}

	// Second call should fail (key already exists).
	ok, err = store.SetNX("alert:dedup:1", 60)
	if err != nil {
		t.Fatalf("SetNX failed: %v", err)
	}
	if ok {
		t.Fatal("expected SetNX to fail on second call")
	}
}

// ---------------------------------------------------------------------------
// Migrations — SQL parsing test (no DB required)
// ---------------------------------------------------------------------------

// Given the embedded migration files, LoadMigrations returns at least one migration
// and the SQL contains the required table names.
func TestMigrations_parseSQL(t *testing.T) {
	migs, err := LoadMigrations()
	if err != nil {
		t.Fatalf("LoadMigrations failed: %v", err)
	}
	if len(migs) == 0 {
		t.Fatal("expected at least one migration")
	}

	requiredTables := []string{"farms", "ponds", "devices", "users", "feeding_logs", "alerts"}

	// Concatenate all migration SQL.
	var allSQL string
	for _, mig := range migs {
		allSQL += mig.SQL + "\n"
	}

	for _, table := range requiredTables {
		pattern := "CREATE TABLE IF NOT EXISTS " + table
		if !strings.Contains(allSQL, pattern) {
			t.Errorf("expected migration SQL to contain %q", pattern)
		}
	}
}

// Given a migration name, sanitizeMigrationName removes single quotes.
func TestSanitizeMigrationName(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"001_init", "001_init"},
		{"001'test", "001test"},
		{"''''", ""},
		{"normal_name", "normal_name"},
	}
	for _, tc := range tests {
		got := SanitizeMigrationName(tc.input)
		if got != tc.expected {
			t.Errorf("SanitizeMigrationName(%q) = %q, want %q", tc.input, got, tc.expected)
		}
	}
}
