// Package health implements the bot's liveness signal: the running process
// refreshes a heartbeat file and a separate invocation checks its age.
package health

import (
	"errors"
	"fmt"
	"os"
	"time"
)

// MaxAge is how stale the heartbeat may be before the bot is unhealthy. It is
// well above the poll interval to tolerate slow cycles.
const MaxAge = 10 * time.Minute

// HeartbeatPath returns the heartbeat file that accompanies the database.
func HeartbeatPath(dbPath string) string {
	return dbPath + ".heartbeat"
}

// Touch records that the bot is alive at now.
func Touch(path string, now time.Time) error {
	if err := os.WriteFile(path, []byte(now.UTC().Format(time.RFC3339)+"\n"), 0o600); err != nil {
		return fmt.Errorf("write heartbeat: %w", err)
	}
	return os.Chtimes(path, now, now)
}

// Check reports an error if the heartbeat is missing or older than maxAge.
func Check(path string, maxAge time.Duration, now time.Time) error {
	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("no heartbeat at %s", path)
		}
		return fmt.Errorf("stat heartbeat: %w", err)
	}
	if age := now.Sub(info.ModTime()); age > maxAge {
		return fmt.Errorf("heartbeat is %s old (limit %s)", age.Round(time.Second), maxAge)
	}
	return nil
}
