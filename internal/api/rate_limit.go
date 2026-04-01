package api

import (
	"math"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/vmsmith/vmsmith/pkg/types"
)

func newIPRateLimiter(rate float64, burst int) *ipRateLimiter {
	if rate <= 0 || burst <= 0 {
		return nil
	}
	return &ipRateLimiter{
		clients: make(map[string]*tokenBucket),
		rate:    rate,
		burst:   burst,
		now:     time.Now,
	}
}

func (l *ipRateLimiter) allow(ip string) (bool, time.Duration) {
	if l == nil || ip == "" {
		return true, 0
	}

	now := l.now()

	l.mu.Lock()
	defer l.mu.Unlock()

	bucket := l.clients[ip]
	if bucket == nil {
		l.clients[ip] = &tokenBucket{tokens: float64(l.burst - 1), last: now}
		return true, 0
	}

	elapsed := now.Sub(bucket.last).Seconds()
	if elapsed > 0 {
		bucket.tokens = math.Min(float64(l.burst), bucket.tokens+elapsed*l.rate)
		bucket.last = now
	}

	if bucket.tokens >= 1 {
		bucket.tokens--
		return true, 0
	}

	neededSeconds := (1 - bucket.tokens) / l.rate
	if neededSeconds < 0 {
		neededSeconds = 0
	}
	return false, time.Duration(math.Ceil(neededSeconds * float64(time.Second)))
}

func (s *Server) rateLimitMiddleware(next http.Handler) http.Handler {
	if s.rateLimiter == nil {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := clientIP(r)
		allowed, retryAfter := s.rateLimiter.allow(ip)
		if allowed {
			next.ServeHTTP(w, r)
			return
		}
		if retryAfter <= 0 {
			retryAfter = time.Second
		}
		w.Header().Set("Retry-After", itoa(int(math.Ceil(retryAfter.Seconds()))))
		writeAPIError(w, http.StatusTooManyRequests, types.NewAPIError("rate_limit_exceeded", "too many API requests from this client; retry shortly"))
	})
}

func clientIP(r *http.Request) string {
	for _, header := range []string{"X-Forwarded-For", "X-Real-IP"} {
		if value := strings.TrimSpace(r.Header.Get(header)); value != "" {
			if header == "X-Forwarded-For" {
				value = strings.TrimSpace(strings.Split(value, ",")[0])
			}
			return value
		}
	}

	host, _, err := net.SplitHostPort(strings.TrimSpace(r.RemoteAddr))
	if err == nil && host != "" {
		return host
	}
	return strings.TrimSpace(r.RemoteAddr)
}
