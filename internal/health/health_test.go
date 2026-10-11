package health

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCheck(t *testing.T) {
	path := HeartbeatPath(filepath.Join(t.TempDir(), "bot.db"))
	now := time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)

	require.ErrorContains(t, Check(path, MaxAge, now), "no heartbeat")

	require.NoError(t, Touch(path, now))
	assert.NoError(t, Check(path, MaxAge, now.Add(MaxAge)))
	assert.ErrorContains(t, Check(path, MaxAge, now.Add(MaxAge+time.Second)), "old")
}
