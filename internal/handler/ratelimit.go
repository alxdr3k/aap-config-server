package handler

import (
	"fmt"
	"math"
	"net/http"

	"golang.org/x/time/rate"
)

// RateLimit configures one endpoint-group token bucket. A zero value disables
// the limiter so existing deployments keep their current behavior unless they
// explicitly opt in.
type RateLimit struct {
	RequestsPerSecond float64
	Burst             int
}

// Enabled reports whether both token-bucket knobs are set.
func (c RateLimit) Enabled() bool {
	return c.RequestsPerSecond > 0 && c.Burst > 0
}

// RateLimitSettings groups the public limiter knobs by the API surfaces that
// carry higher operational cost or secret/admin sensitivity.
type RateLimitSettings struct {
	Admin         RateLimit
	SecretResolve RateLimit
	Watch         RateLimit
	Batch         RateLimit
}

type endpointRateLimiters struct {
	admin         *rate.Limiter
	secretResolve *rate.Limiter
	watch         *rate.Limiter
	batch         *rate.Limiter
}

func newEndpointRateLimiters(settings RateLimitSettings) endpointRateLimiters {
	return endpointRateLimiters{
		admin:         newRateLimiter(settings.Admin),
		secretResolve: newRateLimiter(settings.SecretResolve),
		watch:         newRateLimiter(settings.Watch),
		batch:         newRateLimiter(settings.Batch),
	}
}

func newRateLimiter(cfg RateLimit) *rate.Limiter {
	if !cfg.Enabled() {
		return nil
	}
	return rate.NewLimiter(rate.Limit(cfg.RequestsPerSecond), cfg.Burst)
}

func (h *Handler) limitAdmin(next http.HandlerFunc) http.HandlerFunc {
	return h.limitEndpoint(h.rateLimiters.admin, next)
}

func (h *Handler) limitWatch(next http.HandlerFunc) http.HandlerFunc {
	return h.limitEndpoint(h.rateLimiters.watch, next)
}

func (h *Handler) limitBatch(next http.HandlerFunc) http.HandlerFunc {
	return h.limitEndpoint(h.rateLimiters.batch, next)
}

func (h *Handler) limitEndpoint(limiter *rate.Limiter, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !h.allowRateLimited(w, limiter) {
			return
		}
		next(w, r)
	}
}

func (h *Handler) allowSecretResolve(w http.ResponseWriter) bool {
	return h.allowRateLimited(w, h.rateLimiters.secretResolve)
}

func (h *Handler) allowRateLimited(w http.ResponseWriter, limiter *rate.Limiter) bool {
	if limiter == nil || limiter.Allow() {
		return true
	}
	w.Header().Set("Retry-After", retryAfterSeconds(limiter))
	respondErrorCode(w, http.StatusTooManyRequests, "rate_limited", "rate limit exceeded")
	return false
}

// retryAfterSeconds returns the Retry-After header value derived from the
// limiter's token-fill rate. The minimum is "1" so low-RPS (< 1 req/s)
// limiters still report a useful wait estimate.
func retryAfterSeconds(l *rate.Limiter) string {
	secs := math.Ceil(1.0 / float64(l.Limit()))
	if secs < 1 {
		secs = 1
	}
	return fmt.Sprintf("%d", int(secs))
}
