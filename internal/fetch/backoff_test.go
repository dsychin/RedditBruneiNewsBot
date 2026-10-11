package fetch

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

var t0 = time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

func fixedRand(v float64) func() float64 { return func() float64 { return v } }

// schedule follows retries from the first failure, returning each delay.
func schedule(p Policy, postCreatedAt time.Time) []time.Duration {
	var delays []time.Duration
	now := t0
	for failures := 1; ; failures++ {
		next, ok := p.Next(now, t0, postCreatedAt, failures, 0)
		if !ok {
			return delays
		}
		delays = append(delays, next.Sub(now))
		now = next
	}
}

func TestScheduleDoublesFromBaseAndLandsAtWindow(t *testing.T) {
	p := Policy{Base: time.Minute, Window: 8 * time.Hour, Rand: fixedRand(0.5)}

	delays := schedule(p, t0)

	want := []time.Duration{}
	for d := time.Minute; d <= 128*time.Minute; d *= 2 {
		want = append(want, d)
	}
	want = append(want, 8*time.Hour-255*time.Minute)
	assert.Equal(t, want, delays)
	assert.Len(t, delays, 9, "9 retries, so 10 attempts in total")

	var total time.Duration
	for _, d := range delays {
		total += d
	}
	assert.Equal(t, 8*time.Hour, total)
}

func TestJitterStaysWithinTwentyPercent(t *testing.T) {
	base := 10 * time.Minute
	low, _ := Policy{Base: base, Window: 8 * time.Hour, Rand: fixedRand(0)}.Next(t0, t0, t0, 1, 0)
	high, _ := Policy{Base: base, Window: 8 * time.Hour, Rand: fixedRand(0.999999)}.Next(t0, t0, t0, 1, 0)

	assert.Equal(t, 8*time.Minute, low.Sub(t0))
	assert.InDelta(t, float64(12*time.Minute), float64(high.Sub(t0)), float64(time.Millisecond))
}

func TestJitterNeverPushesFinalRetryPastWindow(t *testing.T) {
	p := Policy{Base: time.Minute, Window: 8 * time.Hour, Rand: fixedRand(0.999999)}

	now := t0
	for failures := 1; ; failures++ {
		next, ok := p.Next(now, t0, t0, failures, 0)
		if !ok {
			break
		}
		assert.False(t, next.After(t0.Add(8*time.Hour)))
		now = next
	}
}

func TestNoRetryOnceWindowIsUsedUp(t *testing.T) {
	p := Policy{Base: time.Minute, Window: 8 * time.Hour, Rand: fixedRand(0.5)}

	_, ok := p.Next(t0.Add(8*time.Hour), t0, t0, 10, 0)

	assert.False(t, ok)
}

func TestRetryPastPostEligibilityWindowIsDropped(t *testing.T) {
	p := Policy{Base: time.Minute, Window: 8 * time.Hour, Rand: fixedRand(0.5)}
	postCreatedAt := t0.Add(-MaxPostAge + 90*time.Second)

	_, ok := p.Next(t0, t0, postCreatedAt, 1, 0)
	assert.True(t, ok, "1 minute delay is still inside the 24h window")

	_, ok = p.Next(t0, t0, postCreatedAt, 2, 0)
	assert.False(t, ok, "2 minute delay passes the 24h window")
}

func TestRetryAfterRaisesDelayButStaysInsideWindow(t *testing.T) {
	p := Policy{Base: time.Minute, Window: 8 * time.Hour, Rand: fixedRand(0.5)}

	next, ok := p.Next(t0, t0, t0, 1, 30*time.Minute)
	assert.True(t, ok)
	assert.Equal(t, 30*time.Minute, next.Sub(t0))

	next, ok = p.Next(t0, t0, t0, 1, 48*time.Hour)
	assert.True(t, ok)
	assert.Equal(t, 8*time.Hour, next.Sub(t0))
}

func TestFailureCountBelowOneHasNoSchedule(t *testing.T) {
	_, ok := Policy{Base: time.Minute, Window: time.Hour}.Next(t0, t0, t0, 0, 0)
	assert.False(t, ok)
}

func TestDefaultRandomJitterIsBounded(t *testing.T) {
	p := Policy{Base: 10 * time.Minute, Window: 8 * time.Hour}
	for range 50 {
		next, ok := p.Next(t0, t0, t0, 1, 0)
		assert.True(t, ok)
		d := next.Sub(t0)
		assert.GreaterOrEqual(t, d, 8*time.Minute)
		assert.LessOrEqual(t, d, 12*time.Minute)
	}
}
