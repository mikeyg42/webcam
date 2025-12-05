package api

import (
	"net"
	"net/http"
	"sync"
	"time"
)

// RateLimiter implements a simple token bucket rate limiter per IP
type RateLimiter struct {
	requests       map[string]*bucket
	mu             sync.RWMutex
	rate           int           // requests per window
	window         time.Duration // time window
	maxCacheSize   int           // maximum number of IPs to track
	trustForwarded bool          // whether to trust X-Forwarded-For header
}

type bucket struct {
	tokens     int
	lastRefill time.Time
}

// NewRateLimiter creates a new rate limiter
// rate: number of requests allowed per window
// window: time window for rate limiting
func NewRateLimiter(rate int, window time.Duration) *RateLimiter {
	rl := &RateLimiter{
		requests:       make(map[string]*bucket),
		rate:           rate,
		window:         window,
		maxCacheSize:   10000,        // Prevent unbounded memory growth
		trustForwarded: false,         // Don't trust X-Forwarded-For by default
	}

	// Clean up old entries periodically
	go rl.cleanup()

	return rl
}

// Allow checks if a request from the given IP should be allowed
func (rl *RateLimiter) Allow(ip string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()

	b, exists := rl.requests[ip]
	if !exists {
		// Check if we've hit the cache size limit
		if len(rl.requests) >= rl.maxCacheSize {
			// Evict oldest entries (simple LRU-like behavior)
			rl.evictOldest(now)
		}

		// First request from this IP
		rl.requests[ip] = &bucket{
			tokens:     rl.rate - 1,
			lastRefill: now,
		}
		return true
	}

	// Refill tokens based on time elapsed
	elapsed := now.Sub(b.lastRefill)
	if elapsed >= rl.window {
		// Full refill
		b.tokens = rl.rate - 1
		b.lastRefill = now
		return true
	}

	// Check if we have tokens available
	if b.tokens > 0 {
		b.tokens--
		return true
	}

	// Rate limit exceeded
	return false
}

// evictOldest removes the oldest 10% of entries when cache is full
func (rl *RateLimiter) evictOldest(now time.Time) {
	// Remove entries older than 2x the window
	for ip, b := range rl.requests {
		if now.Sub(b.lastRefill) > rl.window*2 {
			delete(rl.requests, ip)
		}
	}

	// If still too full, remove 10% of entries
	if len(rl.requests) >= rl.maxCacheSize {
		toRemove := len(rl.requests) / 10
		removed := 0
		for ip := range rl.requests {
			delete(rl.requests, ip)
			removed++
			if removed >= toRemove {
				break
			}
		}
	}
}

// Middleware wraps an HTTP handler with rate limiting
func (rl *RateLimiter) Middleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ip := getClientIP(r)

		if !rl.Allow(ip) {
			http.Error(w, "Rate limit exceeded. Please try again later.", http.StatusTooManyRequests)
			return
		}

		next(w, r)
	}
}

// getClientIP extracts the client IP from the request
func getClientIP(r *http.Request) string {
	// SECURITY: Do not trust X-Forwarded-For by default
	// This can be spoofed by clients to bypass rate limiting
	// Only use RemoteAddr which comes from the actual TCP connection

	// Strip port from RemoteAddr to get just the IP
	// RemoteAddr is in format "IP:port" - we only want IP for rate limiting
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		// If no port or parse error, use RemoteAddr as-is
		return r.RemoteAddr
	}
	return host
}

// cleanup periodically removes old entries to prevent memory leaks
func (rl *RateLimiter) cleanup() {
	ticker := time.NewTicker(10 * time.Minute)
	defer ticker.Stop()

	for range ticker.C {
		rl.mu.Lock()
		now := time.Now()
		for ip, b := range rl.requests {
			if now.Sub(b.lastRefill) > rl.window*2 {
				delete(rl.requests, ip)
			}
		}
		rl.mu.Unlock()
	}
}
