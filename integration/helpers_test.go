// Package integration implements end-to-end integration tests for
// y-ai-pond's three core flows: edge-to-cloud telemetry, feeding decision,
// and digital twin scenario simulation (T31).
//
// DEVIATION FROM plan T31:
// The plan specifies testcontainers-go (InfluxDB + PostgreSQL + EMQX Docker).
// Docker is NOT available in this environment (see
// .omo/notepads/y-ai-pond/learnings.md). In-memory substitutes are used:
//   - mochi-mqtt in-memory TCP broker (replaces EMQX)
//   - pgxmock (replaces PostgreSQL container)
//   - testInfluxWriter (replaces InfluxDB; same pattern as handler_test.go's fakeInflux)
//
// All MQTT transport is real TCP (mochi broker + paho.golang clients);
// only the storage backends are in-memory stubs.
package integration

import (
	"bytes"
	"context"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/anrror/y-ai-pond/internal/handler"
	mochimqtt "github.com/anrror/y-ai-pond/internal/mqtt"
	"github.com/anrror/y-ai-pond/pkg/auth"
	mqttclient "github.com/anrror/y-ai-pond/pkg/mqtt"
	"github.com/anrror/y-ai-pond/pkg/store"
	"github.com/gin-gonic/gin"
	mochi "github.com/mochi-mqtt/server/v2"
	mochiauth "github.com/mochi-mqtt/server/v2/hooks/auth"
	"github.com/mochi-mqtt/server/v2/listeners"
)

const testAuthSecret = "test-secret-integration-32bytes!!"

// ---------------------------------------------------------------------------
// Mochi MQTT broker helpers
// ---------------------------------------------------------------------------

// startMochiBroker spins up an in-memory mochi MQTT broker on 127.0.0.1:0
// and returns its TCP address and a stop function. Docker/EMQX is
// unavailable on this machine, so mochi-mqtt is the in-memory broker used
// for all MQTT integration tests (following the pattern from
// pkg/mqtt/client_test.go).
func startMochiBroker(t testing.TB) (addr string, stop func()) {
	t.Helper()
	s := mochi.New(&mochi.Options{})
	// mochi defaults to DENY-ALL connections; allow everyone (dev/test only).
	if err := s.AddHook(new(mochiauth.AllowHook), nil); err != nil {
		t.Fatalf("add allow hook: %v", err)
	}
	tcp := listeners.NewTCP(listeners.Config{
		ID:      "t1",
		Address: "127.0.0.1:0",
	})
	if err := s.AddListener(tcp); err != nil {
		t.Fatalf("add listener: %v", err)
	}
	go func() { _ = s.Serve() }()
	// Wait until the listener is bound so Address() returns a real port.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if tcp.Address() != "127.0.0.1:0" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	var once sync.Once
	return tcp.Address(), func() {
		once.Do(func() { _ = s.Close() })
	}
}

// newBrokerMQTTClient creates a pkg/mqtt.Client connected to the given
// broker address and waits for the first CONNACK.
func newBrokerMQTTClient(t testing.TB, addr, clientID string, handler mqttclient.MessageHandler) *mqttclient.Client {
	t.Helper()
	c := mqttclient.New(mqttclient.Config{
		BrokerURL: "tcp://" + addr,
		ClientID:  clientID,
	}, handler, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := c.Connect(ctx); err != nil {
		t.Fatalf("connect mqtt client %q: %v", clientID, err)
	}
	return c
}

// ---------------------------------------------------------------------------
// InfluxDB stub
// ---------------------------------------------------------------------------

// testInfluxWriter implements store.InfluxWriter by collecting written
// sensor points in memory. It mirrors the fakeInflux pattern from
// internal/handler/handler_test.go.
type testInfluxWriter struct {
	mu     sync.Mutex
	points []store.SensorPoint
	query  []store.Point // pre-seeded query results
}

func newTestInfluxWriter() *testInfluxWriter {
	return &testInfluxWriter{}
}

func (w *testInfluxWriter) WriteSensorData(_ context.Context, pts []store.SensorPoint) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.points = append(w.points, pts...)
	return nil
}

func (w *testInfluxWriter) QueryTimeRange(_ context.Context, _, _, _ string) ([]store.Point, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	// Return pre-seeded results if set; otherwise convert collected points.
	if w.query != nil {
		return w.query, nil
	}
	var out []store.Point
	for _, sp := range w.points {
		out = append(out, store.Point{
			Timestamp: sp.Timestamp,
			Tags:      map[string]string{"farm_id": sp.FarmID, "pond_id": sp.PondID, "sensor_type": sp.SensorType},
			Fields:    sp.Fields,
		})
	}
	return out, nil
}

func (w *testInfluxWriter) Close() error { return nil }

// snapshot returns a copy of the currently collected points (for assertions).
func (w *testInfluxWriter) snapshot() []store.SensorPoint {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := make([]store.SensorPoint, len(w.points))
	copy(out, w.points)
	return out
}

// ---------------------------------------------------------------------------
// HTTP handler helpers
// ---------------------------------------------------------------------------

// setupTestHandler creates a Handler with test auth, following the pattern
// from internal/handler/handler_test.go (newTestHandler).
func setupTestHandler(t testing.TB, pool store.PgxPool, influx store.InfluxWriter) (*handler.Handler, *auth.AuthService) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	svc := auth.NewAuthService(auth.Config{Secret: testAuthSecret, Expiration: time.Hour})
	h := handler.NewHandler(pool, influx, svc, nil)
	return h, svc
}

// newTestRouter creates a Gin engine with all handler routes registered.
func newTestRouter(t testing.TB, h *handler.Handler) *gin.Engine {
	t.Helper()
	r := gin.New()
	h.RegisterRoutes(r)
	return r
}

// authToken generates a Bearer JWT for a test user.
func authToken(t testing.TB, svc *auth.AuthService, farmIDs []string) string {
	t.Helper()
	pair, err := svc.IssueToken(&auth.User{ID: "integration-user", Role: auth.RoleAdmin, FarmIDs: farmIDs})
	if err != nil {
		t.Fatalf("issue token: %v", err)
	}
	return "Bearer " + pair.AccessToken
}

// doRequest performs an HTTP request against the test router.
func doRequest(t testing.TB, r *gin.Engine, method, path, token, body string) *httptest.ResponseRecorder {
	t.Helper()
	var reader *bytes.Reader
	if body == "" {
		reader = bytes.NewReader(nil)
	} else {
		reader = bytes.NewReader([]byte(body))
	}
	req := httptest.NewRequest(method, path, reader)
	if token != "" {
		req.Header.Set("Authorization", token)
	}
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

// ---------------------------------------------------------------------------
// Gateway helpers
// ---------------------------------------------------------------------------

// newTestGateway creates a cloud MQTT gateway wired to an in-memory influx
// writer and a pgxmock pool. The caller is responsible for connecting the
// gateway's MQTT client to the broker and calling Start.
func newTestGateway(influx store.InfluxWriter, pg store.PgxPool) *mochimqtt.Gateway {
	gw := mochimqtt.NewGateway(nil, influx, pg, nil) // nil logger => slog.Default()
	return gw
}

// Ensure interface compliance.
var _ store.InfluxWriter = (*testInfluxWriter)(nil)

// assertFloatApprox fails if got and want differ by more than an epsilon
// that accounts for float32→float64 precision loss in protobuf round-trips.
func assertFloatApprox(t testing.TB, label string, got, want float64) {
	t.Helper()
	const eps = 1e-5
	diff := got - want
	if diff < 0 {
		diff = -diff
	}
	if diff > eps {
		t.Errorf("%s = %.10f, want %.10f (diff %.2e > eps %.0e)", label, got, want, diff, eps)
	}
}
