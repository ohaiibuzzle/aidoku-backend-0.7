package host

import (
	"sync"
	"time"
)

// RateLimit mirrors AidokuRunner's RateLimit.swift: a simple fixed-window
// limiter that blocks the calling goroutine until a permit is free. Unlike
// the Swift original (an actor bridged into synchronous host callbacks via
// BlockingTask), Go host functions already run synchronously on the
// calling goroutine, so this can block directly with no bridging needed.
type RateLimit struct {
	mu sync.Mutex

	permits int
	period  time.Duration

	currentPeriodStart time.Time
	requestsInPeriod   int
}

func (r *RateLimit) Set(permits int, period time.Duration) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.permits = permits
	r.period = period
}

func (r *RateLimit) enabled() bool {
	return r.permits > 0 && r.period > 0
}

func (r *RateLimit) inPeriod(now time.Time) bool {
	return now.Sub(r.currentPeriodStart) < r.period
}

func (r *RateLimit) atLimit(now time.Time) bool {
	return r.inPeriod(now) && r.requestsInPeriod >= r.permits
}

// Wait blocks until a permit is available, then consumes one.
func (r *RateLimit) Wait() {
	for {
		r.mu.Lock()
		if !r.enabled() {
			r.mu.Unlock()
			return
		}
		now := time.Now()
		if r.atLimit(now) {
			wait := r.currentPeriodStart.Add(r.period).Sub(now)
			r.mu.Unlock()
			if wait > 0 {
				time.Sleep(wait)
			}
			continue
		}
		if !r.inPeriod(now) {
			r.currentPeriodStart = now
			r.requestsInPeriod = 0
		}
		r.requestsInPeriod++
		r.mu.Unlock()
		return
	}
}
