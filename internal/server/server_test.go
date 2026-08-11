package server

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

// recorder captures lifecycle events in shared order.
type recorder struct {
	mu    sync.Mutex
	order []string
}

func (r *recorder) add(ev string) {
	r.mu.Lock()
	r.order = append(r.order, ev)
	r.mu.Unlock()
}

func (r *recorder) getAll() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, len(r.order))
	copy(out, r.order)
	return out
}

// recordingModule records lifecycle call order.
type recordingModule struct {
	id      string
	rec     *recorder
	initFn  func() error
	startFn func() error
}

func (m *recordingModule) call(ev string) {
	if m.rec != nil {
		m.rec.add(ev)
	}
}

func (m *recordingModule) ID() string { return m.id }
func (m *recordingModule) Init(ctx context.Context) error {
	m.call("init:" + m.id)
	if m.initFn != nil {
		return m.initFn()
	}
	return nil
}
func (m *recordingModule) Start(ctx context.Context) error {
	m.call("start:" + m.id)
	if m.startFn != nil {
		return m.startFn()
	}
	return nil
}
func (m *recordingModule) Stop(ctx context.Context) error {
	m.call("stop:" + m.id)
	return nil
}

func testConfigHTTPPort() {}

var _ = testConfigHTTPPort

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func newTestServer(t *testing.T, mods ...Module) *Server {
	t.Helper()
	s := &Server{log: testLogger(), health: make(map[string]HealthCheckFunc), start: time.Now()}
	s.modules = append(s.modules, mods...)
	gin.SetMode(gin.TestMode)
	s.router = gin.New()
	return s
}

func TestModuleLifecycleOrder(t *testing.T) {
	events := &recorder{order: []string{}}
	a := &recordingModule{id: "a", rec: events}
	b := &recordingModule{id: "b", rec: events}
	s := newTestServer(t, a, b)

	if err := s.initModules(context.Background()); err != nil {
		t.Fatalf("initModules: %v", err)
	}
	if err := s.startModules(context.Background()); err != nil {
		t.Fatalf("startModules: %v", err)
	}

	want := []string{"init:a", "init:b", "start:a", "start:b"}
	got := events.getAll()
	if len(got) != len(want) {
		t.Fatalf("lifecycle calls = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("lifecycle order = %v, want %v", got, want)
		}
	}
}

func TestModuleInitErrorAbortsStartup(t *testing.T) {
	boom := &recordingModule{id: "boom", initFn: func() error { return errors.New("init boom") }}
	s := newTestServer(t, boom)
	if err := s.initModules(context.Background()); err == nil {
		t.Fatal("expected init error, got nil")
	}
}

func TestModuleStartErrorAbortsStartup(t *testing.T) {
	boom := &recordingModule{id: "boom", startFn: func() error { return errors.New("start boom") }}
	s := newTestServer(t, boom)
	if err := s.startModules(context.Background()); err == nil {
		t.Fatal("expected start error, got nil")
	}
}

func TestHealthHandlerAggregatesChecks(t *testing.T) {
	s := newTestServer(t)
	s.RegisterHealthCheck("postgres", func(ctx context.Context) error { return nil })
	s.RegisterHealthCheck("redis", func(ctx context.Context) error { return errors.New("down") })

	s.setupHTTPServer()
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	s.router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("health status = %d, want 200", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, `"status":"degraded"`) {
		t.Fatalf("health body missing degraded status: %s", body)
	}
	if !strings.Contains(body, "postgres") || !strings.Contains(body, "redis") {
		t.Fatalf("health body missing checks: %s", body)
	}
}

func TestMetricsHandlerServesPrometheusText(t *testing.T) {
	s := newTestServer(t)
	s.RegisterHealthCheck("mqtt", func(ctx context.Context) error { return nil })
	s.setupHTTPServer()

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	s.router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("metrics status = %d, want 200", w.Code)
	}
	body := w.Body.String()
	for _, want := range []string{"y_ai_pond_uptime_seconds", "y_ai_pond_goroutines", `component="mqtt"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("metrics body missing %q:\n%s", want, body)
		}
	}
}

func TestShutdownReverseOrder(t *testing.T) {
	events := &recorder{order: []string{}}
	a := &recordingModule{id: "store", rec: events}
	b := &recordingModule{id: "gateway", rec: events}
	s := newTestServer(t, a, b)
	s.setupHTTPServer()

	_ = s.initModules(context.Background())
	_ = s.startModules(context.Background())
	if err := s.shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown: %v", err)
	}

	stops := []string{}
	for _, c := range events.getAll() {
		if strings.HasPrefix(c, "stop:") {
			stops = append(stops, c)
		}
	}
	if len(stops) != 2 || stops[0] != "stop:gateway" || stops[1] != "stop:store" {
		t.Fatalf("stop order = %v, want [stop:gateway stop:store]", stops)
	}
}
