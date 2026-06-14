package db

import (
	"fmt"
	"os"
	"sync"
	"time"
)

// DeleteExpiredSessions removes session rows whose expiry has passed. Sessions
// are also pruned lazily on access (GetSession), but a periodic sweep keeps the
// table from accumulating rows for users who never return.
func DeleteExpiredSessions() (int64, error) {
	res, err := pool.Exec("DELETE FROM sessions WHERE expires_at < ?", NowTS())
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

var maintenanceOnce sync.Once

// StartBackgroundMaintenance launches a single goroutine that periodically
// prunes expired sessions and caps the debug ring buffer. It is safe to call
// from every binary's main: only the first call starts the loop.
func StartBackgroundMaintenance() {
	maintenanceOnce.Do(func() {
		go func() {
			// Run once shortly after startup, then on a steady cadence.
			runMaintenance()
			ticker := time.NewTicker(10 * time.Minute)
			defer ticker.Stop()
			for range ticker.C {
				runMaintenance()
			}
		}()
	})
}

func runMaintenance() {
	if _, err := DeleteExpiredSessions(); err != nil {
		fmt.Fprintf(os.Stderr, "[maintenance] session prune: %v\n", err)
	}
	if err := PruneDebugEvents(DebugCap); err != nil {
		fmt.Fprintf(os.Stderr, "[maintenance] debug prune: %v\n", err)
	}
}
