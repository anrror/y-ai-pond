package server

import (
	"context"
	"net/http"
	"runtime"
	"sort"
	"time"

	"github.com/gin-gonic/gin"
)

// handleHealth reports aggregate component health as JSON. Overall status is
// "ok" only when every registered health check passes; otherwise "degraded".
func (s *Server) handleHealth(c *gin.Context) {
	s.healthMu.Lock()
	names := make([]string, 0, len(s.health))
	for name := range s.health {
		names = append(names, name)
	}
	s.healthMu.Unlock()
	sort.Strings(names)

	type checkResult struct {
		Status  string `json:"status"`
		Error   string `json:"error,omitempty"`
		Latency string `json:"latency,omitempty"`
	}
	checks := make(map[string]checkResult, len(names))
	allOK := true

	for _, name := range names {
		ctx, cancel := context.WithTimeout(c.Request.Context(), 2*time.Second)
		started := time.Now()
		err := s.health[name](ctx)
		latency := time.Since(started)
		cancel()

		res := checkResult{Status: "ok", Latency: latency.Round(time.Microsecond).String()}
		if err != nil {
			res.Status = "down"
			res.Error = err.Error()
			allOK = false
		}
		checks[name] = res
	}

	status := "ok"
	if !allOK {
		status = "degraded"
	}

	c.JSON(http.StatusOK, gin.H{
		"status":    status,
		"timestamp": time.Now().UTC().Format(time.RFC3339),
		"uptime_s":  int64(time.Since(s.start).Seconds()),
		"checks":    checks,
	})
}

// handleMetrics serves process and component metrics in Prometheus text format
// (exposition 0.0.4). Scope: runtime + module health gauges — application
// metrics are added by future modules.
func (s *Server) handleMetrics(c *gin.Context) {
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)

	s.healthMu.Lock()
	names := make([]string, 0, len(s.health))
	for name := range s.health {
		names = append(names, name)
	}
	s.healthMu.Unlock()
	sort.Strings(names)

	body := "# HELP y_ai_pond_uptime_seconds Server uptime in seconds.\n" +
		"# TYPE y_ai_pond_uptime_seconds gauge\n" +
		"y_ai_pond_uptime_seconds " + itoa(int64(time.Since(s.start).Seconds())) + "\n" +
		"# HELP y_ai_pond_goroutines Number of goroutines.\n" +
		"# TYPE y_ai_pond_goroutines gauge\n" +
		"y_ai_pond_goroutines " + itoa(int64(runtime.NumGoroutine())) + "\n" +
		"# HELP y_ai_pond_heap_bytes Current heap allocation in bytes.\n" +
		"# TYPE y_ai_pond_heap_bytes gauge\n" +
		"y_ai_pond_heap_bytes " + itoa(int64(ms.HeapAlloc)) + "\n" //nolint:gosec // G115: HeapAlloc fits int64 on all platforms

	for _, name := range names {
		ctx, cancel := context.WithTimeout(c.Request.Context(), 2*time.Second)
		err := s.health[name](ctx)
		cancel()
		up := 0
		if err == nil {
			up = 1
		}
		body += "# HELP y_ai_pond_component_up Whether " + name + " is healthy.\n" +
			"# TYPE y_ai_pond_component_up gauge\n" +
			"y_ai_pond_component_up{component=\"" + name + "\"} " + itoa(int64(up)) + "\n"
	}

	c.Data(http.StatusOK, "text/plain; version=0.0.4; charset=utf-8", []byte(body))
}

func itoa(v int64) string {
	if v == 0 {
		return "0"
	}
	neg := v < 0
	if neg {
		v = -v
	}
	var b [20]byte
	i := len(b)
	for v > 0 {
		i--
		b[i] = byte('0' + v%10)
		v /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}
