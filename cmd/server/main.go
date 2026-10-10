// Command server is the user-facing Alex Messages app.
//
// It runs DB + admin + VAPID bootstrap, then serves the Gin engine. The listen
// address comes from HOST/PORT (defaults 0.0.0.0:8765 for local dev; use port
// 80 in production).
package main

import (
	"log"
	"os"

	"github.com/gin-gonic/gin"

	"alexmessage/internal/auth"
	"alexmessage/internal/db"
	"alexmessage/internal/httpx"
	"alexmessage/internal/matrix"
	"alexmessage/internal/push"
	"alexmessage/internal/webapp"
)

func main() {
	gin.SetMode(gin.ReleaseMode)

	if err := db.InitDB(); err != nil {
		log.Fatalf("db init: %v", err)
	}
	if err := auth.BootstrapAdmin(); err != nil {
		log.Fatalf("admin bootstrap: %v", err)
	}
	if err := push.BootstrapVAPID(); err != nil {
		log.Fatalf("vapid bootstrap: %v", err)
	}
	db.StartBackgroundMaintenance()

	// Matrix appservice bridge: no-op unless the MATRIX_* env vars are set.
	if _, err := matrix.Start(); err != nil {
		log.Fatalf("matrix bridge: %v", err)
	}

	addr := envOr("HOST", "0.0.0.0") + ":" + envOr("PORT", "8765")
	if err := httpx.Serve("alex-messages", addr, webapp.NewEngine()); err != nil {
		log.Fatalf("server: %v", err)
	}
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
