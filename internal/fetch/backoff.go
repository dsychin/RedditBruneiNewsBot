package fetch

import (
	"math/rand/v2"
	"time"
)

// MaxPostAge is how long a post stays eligible for a reply.
const MaxPostAge = 24 * time.Hour

const jitterFraction = 0.2

// Policy schedules retries with exponential backoff and jitter. Delays double
// from Base; the last delay is clamped so the final retry lands at Window after
// the first failure.
type Policy struct {
	Base   time.Duration
	Window time.Duration
	// Rand returns a value in [0, 1). It defaults to math/rand/v2.
	Rand func() float64
}

// Next returns when to retry after the failures-th failed attempt (1-based).
// firstFailedAt is the time of the first failure and postCreatedAt is when the
// Reddit post was created. retryAfter is a server-requested minimum delay (0 if
// none). It reports false when no further retry should be scheduled.
func (p Policy) Next(now, firstFailedAt, postCreatedAt time.Time, failures int, retryAfter time.Duration) (time.Time, bool) {
	deadline := firstFailedAt.Add(p.Window)
	remaining := deadline.Sub(now)
	if failures < 1 || remaining <= 0 {
		return time.Time{}, false
	}

	delay := p.Base << min(failures-1, 30)
	delay = time.Duration(float64(delay) * p.jitter())
	delay = max(delay, retryAfter)
	delay = min(delay, remaining)

	next := now.Add(delay)
	if next.After(postCreatedAt.Add(MaxPostAge)) {
		return time.Time{}, false
	}
	return next, true
}

func (p Policy) jitter() float64 {
	r := rand.Float64
	if p.Rand != nil {
		r = p.Rand
	}
	return 1 + jitterFraction*(2*r()-1)
}
