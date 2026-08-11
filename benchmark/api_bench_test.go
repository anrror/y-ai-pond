package benchmark

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/anrror/y-ai-pond/internal/handler"
	"github.com/anrror/y-ai-pond/pkg/auth"
	"github.com/anrror/y-ai-pond/pkg/store"
	"github.com/gin-gonic/gin"
	"github.com/pashagolub/pgxmock/v4"
)

// benchmarkFakeInflux is a fast in-memory InfluxWriter for API benchmarks.
type benchmarkFakeInflux struct {
	points []store.Point
}

func (f *benchmarkFakeInflux) WriteSensorData(_ context.Context, _ []store.SensorPoint) error { return nil }
func (f *benchmarkFakeInflux) QueryTimeRange(_ context.Context, _, _, _ string) ([]store.Point, error) {
	return f.points, nil
}
func (f *benchmarkFakeInflux) Close() error { return nil }

var _ store.InfluxWriter = (*benchmarkFakeInflux)(nil)

const benchAuthSecret = "bench-secret-key-32chars-min!!!"

// BenchmarkAPI measures concurrent HTTP GET /api/v1/sensors/latest latency.
// Target: p95 < 200ms at 1000 req/s.
//
//	Uses Gin test router with pgxmock + fake Influx.
//	Fires concurrent requests and measures full round-trip latency.
func BenchmarkAPI(b *testing.B) {
	gin.SetMode(gin.TestMode)

	now := time.Now()
	influx := &benchmarkFakeInflux{points: []store.Point{
		{Tags: map[string]string{"farm_id": "farm-1", "pond_id": "pond-1", "sensor_type": "ph"}, Timestamp: now.Add(-time.Minute), Fields: map[string]float64{"ph": 7.2}},
		{Tags: map[string]string{"farm_id": "farm-1", "pond_id": "pond-1", "sensor_type": "do"}, Timestamp: now.Add(-30 * time.Second), Fields: map[string]float64{"do": 6.5}},
		{Tags: map[string]string{"farm_id": "farm-1", "pond_id": "pond-1", "sensor_type": "temp"}, Timestamp: now.Add(-10 * time.Second), Fields: map[string]float64{"temp": 25.3}},
	}}

	pool, err := pgxmock.NewPool()
	if err != nil {
		b.Fatalf("pgxmock: %v", err)
	}
	defer pool.Close()

	svc := auth.NewAuthService(auth.Config{Secret: benchAuthSecret, Expiration: time.Hour})
	h := handler.NewHandler(pool, influx, svc, nil)
	r := gin.New()
	h.RegisterRoutes(r)

	tokenPair, err := svc.IssueToken(&auth.User{ID: "bench-user", Role: auth.RoleAdmin, FarmIDs: []string{"farm-1"}})
	if err != nil {
		b.Fatalf("issue token: %v", err)
	}
	authHdr := "Bearer " + tokenPair.AccessToken

	const path = "/api/v1/sensors/latest?pond_id=pond-1"

	b.ResetTimer()

	allLatencies := make([]time.Duration, 0, b.N)
	var mu sync.Mutex
	var wg sync.WaitGroup

	for i := 0; i < b.N; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			req := httptest.NewRequest(http.MethodGet, path, nil)
			req.Header.Set("Authorization", authHdr)
			rec := httptest.NewRecorder()

			start := time.Now()
			r.ServeHTTP(rec, req)
			elapsed := time.Since(start)

			if rec.Code != http.StatusOK {
				b.Errorf("unexpected status %d: %s", rec.Code, rec.Body.String())
			}

			mu.Lock()
			allLatencies = append(allLatencies, elapsed)
			mu.Unlock()
		}()
	}

	wg.Wait()

	reportLatencyPercentiles(b, allLatencies)
}
