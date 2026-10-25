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

var (
	extraMu    sync.Mutex
	extraTasks []func()
)

// AddMaintenance adds fn to every later sweep of this process. A binary
// registers its own tasks (cmd/server prunes the OTA install census) before
// it calls StartBackgroundMaintenance.
func AddMaintenance(fn func()) {
	extraMu.Lock()
	defer extraMu.Unlock()
	extraTasks = append(extraTasks, fn)
}

// StartBackgroundMaintenance launches a single goroutine that periodically
// deletes unreferenced uploads older than 24 hours (PruneUnusedUploads),
// prunes expired sessions, caps the debug ring buffer, and runs the tasks
// added with AddMaintenance. It is safe to call from every binary's main:
// only the first call starts the loop.
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
	if err := PruneUnusedUploads(time.Now()); err != nil {
		fmt.Fprintf(os.Stderr, "[maintenance] upload prune: %v\n", err)
	}
	if _, err := DeleteExpiredSessions(); err != nil {
		fmt.Fprintf(os.Stderr, "[maintenance] session prune: %v\n", err)
	}
	if err := PruneDebugEvents(DebugCap); err != nil {
		fmt.Fprintf(os.Stderr, "[maintenance] debug prune: %v\n", err)
	}
	extraMu.Lock()
	tasks := append([]func(){}, extraTasks...)
	extraMu.Unlock()
	for _, fn := range tasks {
		fn()
	}
}
