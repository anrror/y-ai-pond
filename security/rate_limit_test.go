package security

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/anrror/y-ai-pond/internal/middleware"
	"github.com/gin-gonic/gin"
)

// TestRateLimit_ExceededReturns429 verifies that when requests exceed
// the configured rate limit (100 req/min), the middleware returns
// HTTP 429 Too Many Requests.
//
// Acceptance criteria (T33): "TestRateLimit > 100 req/min → 429"
func TestRateLimit_ExceededReturns429(t *testing.T) {
	gin.SetMode(gin.TestMode)

	// Create a router with rate limiting at 100 req/min.
	r := gin.New()
	r.Use(middleware.RateLimit(100))
	r.GET("/test", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	// Send 100 requests at the maximum rate — all should pass.
	for i := 0; i < 100; i++ {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/test", nil)
		req.RemoteAddr = "192.168.1.100:54321"
		r.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("request %d: unexpected status %d (expected 200 before limit)", i+1, w.Code)
		}
	}

	// Request 101: should be rate limited (429).
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.RemoteAddr = "192.168.1.100:54321"
	r.ServeHTTP(w, req)

	if w.Code != http.StatusTooManyRequests {
		t.Errorf("request 101: status = %d, want 429 Too Many Requests", w.Code)
	}
	if retryAfter := w.Header().Get("Retry-After"); retryAfter == "" {
		t.Error("request 101: missing Retry-After header")
	}
	if body := w.Body.String(); body == "" {
		t.Error("request 101: empty response body")
	}
}

// TestRateLimit_DifferentIPsIndependent verifies that rate limits are
// per-IP: exceeding the limit on one IP does not affect another.
func TestRateLimit_DifferentIPsIndependent(t *testing.T) {
	gin.SetMode(gin.TestMode)

	r := gin.New()
	r.Use(middleware.RateLimit(100))
	r.GET("/test", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	// Exhaust IP 1's quota.
	for i := 0; i < 100; i++ {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/test", nil)
		req.RemoteAddr = "10.0.0.1:11111"
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("IP1 request %d: unexpected status %d", i+1, w.Code)
		}
	}

	// IP 1 should now be rate-limited.
	w1 := httptest.NewRecorder()
	req1 := httptest.NewRequest(http.MethodGet, "/test", nil)
	req1.RemoteAddr = "10.0.0.1:11111"
	r.ServeHTTP(w1, req1)
	if w1.Code != http.StatusTooManyRequests {
		t.Errorf("IP1 after 100: status = %d, want 429", w1.Code)
	}

	// IP 2 should still be allowed (independent bucket).
	w2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodGet, "/test", nil)
	req2.RemoteAddr = "10.0.0.2:22222"
	r.ServeHTTP(w2, req2)
	if w2.Code != http.StatusOK {
		t.Errorf("IP2: status = %d, want 200 (independent limit)", w2.Code)
	}
}

// TestRateLimit_RefillAfterWait verifies that the token bucket refills
// over time, allowing requests again after a waiting period.
func TestRateLimit_RefillAfterWait(t *testing.T) {
	gin.SetMode(gin.TestMode)

	// Use a small rate (10 req/sec equivalent → 600 req/min) so we can test
	// refill within a reasonable test timeout.
	r := gin.New()
	r.Use(middleware.RateLimit(600)) // 10 tokens/sec
	r.GET("/test", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	ip := "172.16.0.1:9999"

	// Consume all 600 tokens instantly.
	for i := 0; i < 600; i++ {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/test", nil)
		req.RemoteAddr = ip
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("request %d: unexpected status %d", i+1, w.Code)
		}
	}

	// Next request should be rate-limited.
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.RemoteAddr = ip
	r.ServeHTTP(w, req)
	if w.Code != http.StatusTooManyRequests {
		t.Fatal("expected 429 after exhausting tokens, but request was allowed")
	}

	// Wait for ~200ms — at 10 tokens/sec, that refills ~2 tokens.
	time.Sleep(200 * time.Millisecond)

	// Should now be able to make at least 1 request.
	w2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodGet, "/test", nil)
	req2.RemoteAddr = ip
	r.ServeHTTP(w2, req2)
	if w2.Code != http.StatusOK {
		t.Errorf("after 200ms refill: status = %d, want 200 (refilled tokens available)", w2.Code)
	}
}

// TestRateLimit_DefaultValues verifies that RateLimit(0) uses the default
// of 100 req/min and that negative values also default.
func TestRateLimit_DefaultValues(t *testing.T) {
	gin.SetMode(gin.TestMode)

	testCases := []struct {
		name string
		rpm  int
	}{
		{"zero uses default", 0},
		{"negative uses default", -1},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			r := gin.New()
			r.Use(middleware.RateLimit(tc.rpm))
			r.GET("/test", func(c *gin.Context) {
				c.JSON(http.StatusOK, gin.H{"status": "ok"})
			})

			// Make 100 requests — all should pass (default = 100).
			for i := 0; i < 100; i++ {
				w := httptest.NewRecorder()
				req := httptest.NewRequest(http.MethodGet, "/test", nil)
				req.RemoteAddr = "192.168.2.1:11111"
				r.ServeHTTP(w, req)
				if w.Code != http.StatusOK {
					t.Fatalf("request %d: unexpected status %d", i+1, w.Code)
				}
			}

			// 101st should fail.
			w := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/test", nil)
			req.RemoteAddr = "192.168.2.1:11111"
			r.ServeHTTP(w, req)
			if w.Code != http.StatusTooManyRequests {
				t.Errorf("request 101: status = %d, want 429", w.Code)
			}
		})
	}
}

// TestRateLimit_ConcurrentAccess verifies thread safety under concurrent
// requests from multiple goroutines sharing the same IP.
func TestRateLimit_ConcurrentAccess(t *testing.T) {
	gin.SetMode(gin.TestMode)

	r := gin.New()
	r.Use(middleware.RateLimit(200)) // 200 req/min burst
	r.GET("/test", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	ip := "10.10.10.10:12345"
	const goroutines = 20
	const reqPerGoroutine = 15 // total = 300, but burst = 200 → some should get 429

	var wg sync.WaitGroup
	results := make(chan int, goroutines*reqPerGoroutine)

	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < reqPerGoroutine; i++ {
				w := httptest.NewRecorder()
				req := httptest.NewRequest(http.MethodGet, "/test", nil)
				req.RemoteAddr = ip
				r.ServeHTTP(w, req)
				results <- w.Code
			}
		}()
	}
	wg.Wait()
	close(results)

	okCount := 0
	limitedCount := 0
	for code := range results {
		switch code {
		case http.StatusOK:
			okCount++
		case http.StatusTooManyRequests:
			limitedCount++
		default:
			t.Errorf("unexpected status code: %d", code)
		}
	}

	// At least some requests should succeed (burst allows up to 200).
	if okCount == 0 {
		t.Error("no requests succeeded — all were rate-limited")
	}
	// Some requests should be rate-limited (total 300 > burst 200).
	if limitedCount == 0 {
		t.Error("no requests were rate-limited — expected some 429s")
	}

	t.Logf("concurrent results: %d OK, %d rate-limited (total %d)", okCount, limitedCount, okCount+limitedCount)
}
