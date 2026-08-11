// Package middleware provides Gin HTTP middleware for rate limiting using
// a token-bucket algorithm per client IP.
//
// RateLimit creates a middleware that enforces a maximum request rate per IP.
// When the limit is exceeded, the middleware returns HTTP 429 with a
// Retry-After header.
package middleware

import (
	"fmt"
	"math"
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

// DefaultRateLimit is the default maximum requests per minute per IP (100).
const DefaultRateLimit = 100

// cleanupInterval is how often stale buckets are pruned from the map.
const cleanupInterval = 5 * time.Minute

// bucketTTL is the maximum time an idle bucket is kept before cleanup.
const bucketTTL = 15 * time.Minute

// tokenBucket tracks available tokens and the last refill time for a single IP.
type tokenBucket struct {
	tokens     float64
	lastRefill time.Time
}

// rateLimiter implements a per-IP token-bucket rate limiter.
type rateLimiter struct {
	mu        sync.Mutex
	buckets   map[string]*tokenBucket
	rate      float64 // tokens added per second
	burst     float64 // maximum tokens
	lastClean time.Time
}

// newRateLimiter creates a per-IP token-bucket limiter.
// requestsPerMinute is the sustained rate; burst is set equal to the rate
// (a burst of n tokens allows n instantaneous requests followed by a
// rate-limited refill).
func newRateLimiter(requestsPerMinute int) *rateLimiter {
	rps := float64(requestsPerMinute) / 60.0
	return &rateLimiter{
		buckets: make(map[string]*tokenBucket),
		rate:    rps,
		burst:   float64(requestsPerMinute),
	}
}

// allow checks whether a request from ip is allowed. Returns true and
// the number of remaining tokens when allowed; returns false and the
// suggested retry-after duration when denied.
func (rl *rateLimiter) allow(ip string) (bool, time.Duration) {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	rl.maybeCleanup(now)

	b, ok := rl.buckets[ip]
	if !ok {
		b = &tokenBucket{tokens: rl.burst - 1, lastRefill: now}
		rl.buckets[ip] = b
		return true, 0
	}

	// Refill tokens based on elapsed time.
	elapsed := now.Sub(b.lastRefill).Seconds()
	b.tokens = math.Min(b.tokens+elapsed*rl.rate, rl.burst)
	b.lastRefill = now

	if b.tokens >= 1.0 {
		b.tokens--
		return true, 0
	}

	// Calculate wait time until next token is available.
	wait := time.Duration(math.Ceil((1.0-b.tokens)/rl.rate)) * time.Second
	return false, wait
}

// maybeCleanup prunes stale buckets periodically.
func (rl *rateLimiter) maybeCleanup(now time.Time) {
	if now.Sub(rl.lastClean) < cleanupInterval {
		return
	}
	rl.lastClean = now
	for ip, b := range rl.buckets {
		if now.Sub(b.lastRefill) > bucketTTL {
			delete(rl.buckets, ip)
		}
	}
}

// RateLimit returns a Gin middleware that enforces a per-IP token-bucket
// rate limit. When requestsPerMinute is zero or negative, DefaultRateLimit
// (100 req/min) is used.
//
// The client IP is extracted via gin.Context.ClientIP(), which respects
// X-Forwarded-For and X-Real-IP headers when the Gin engine is configured
// with SetTrustedProxies.
func RateLimit(requestsPerMinute int) gin.HandlerFunc {
	if requestsPerMinute <= 0 {
		requestsPerMinute = DefaultRateLimit
	}
	rl := newRateLimiter(requestsPerMinute)

	return func(c *gin.Context) {
		ip := c.ClientIP()
		allowed, retryAfter := rl.allow(ip)
		if !allowed {
			c.Header("Retry-After", fmt.Sprintf("%.0f", math.Ceil(retryAfter.Seconds())))
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{
				"error": "rate limit exceeded",
			})
			return
		}
		c.Next()
	}
}
