// Package ratelimit throttles abuse-prone endpoints with in-memory token
// buckets (docs/design.md#rate-limits). Buckets are per replica: with N
// replicas a client gets up to N times each limit. The route table
// (rules.go) names mux patterns; the middleware is installed with
// r.UseApp/r.UseSites, so feature packages never call this package.
package ratelimit

import (
	"math"
	"sync"
	"time"
)

// Limit is a token bucket: Burst requests at once, refilled at PerMinute.
type Limit struct {
	PerMinute float64
	Burst     int
}

// limiter holds one bucket per key for one rule.
type limiter struct {
	limit Limit
	now   func() time.Time

	mu        sync.Mutex
	buckets   map[string]*bucket
	lastSweep time.Time
}

type bucket struct {
	tokens float64
	last   time.Time
}

// maxKeys bounds memory: past it, full (idle) buckets are dropped every
// second instead of every sweepEvery.
const maxKeys = 100_000

// sweepEvery is how often buckets that have refilled are dropped; a
// dropped bucket is indistinguishable from a full one.
const sweepEvery = time.Minute

func newLimiter(l Limit, now func() time.Time) *limiter {
	return &limiter{limit: l, now: now, buckets: map[string]*bucket{}, lastSweep: now()}
}

func (l *limiter) rate() float64 { return l.limit.PerMinute / 60 }

// allow takes a token from key's bucket. When the bucket is empty it
// returns false and how long until a token is available.
func (l *limiter) allow(key string) (bool, time.Duration) {
	now := l.now()
	l.mu.Lock()
	defer l.mu.Unlock()
	if d := now.Sub(l.lastSweep); d >= sweepEvery || (len(l.buckets) >= maxKeys && d >= time.Second) {
		l.sweep(now)
	}
	b, ok := l.buckets[key]
	if !ok {
		b = &bucket{tokens: float64(l.limit.Burst), last: now}
		l.buckets[key] = b
	}
	if elapsed := now.Sub(b.last).Seconds(); elapsed > 0 {
		b.tokens = math.Min(float64(l.limit.Burst), b.tokens+elapsed*l.rate())
		b.last = now
	}
	if b.tokens >= 1 {
		b.tokens--
		return true, 0
	}
	wait := (1 - b.tokens) / l.rate()
	return false, time.Duration(wait * float64(time.Second))
}

// sweep drops buckets that would be full by now.
func (l *limiter) sweep(now time.Time) {
	l.lastSweep = now
	for k, b := range l.buckets {
		if b.tokens+now.Sub(b.last).Seconds()*l.rate() >= float64(l.limit.Burst) {
			delete(l.buckets, k)
		}
	}
}
