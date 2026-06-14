// Command admin is the Alex Messages admin control panel.
//
// Runs on its own port (default 8001) sharing the user app's database. The
// listen address comes from ADMIN_HOST/ADMIN_PORT.
package main

import (
	"log"
	"os"

	"github.com/gin-gonic/gin"

	"alexmessage/internal/adminapp"
	"alexmessage/internal/auth"
	"alexmessage/internal/db"
	"alexmessage/internal/httpx"
)

func main() {
	gin.SetMode(gin.ReleaseMode)

	if err := db.InitDB(); err != nil {
		log.Fatalf("db init: %v", err)
	}
	if err := auth.BootstrapAdmin(); err != nil {
		log.Fatalf("admin bootstrap: %v", err)
	}
	db.StartBackgroundMaintenance()

	addr := envOr("ADMIN_HOST", "127.0.0.1") + ":" + envOr("ADMIN_PORT", "8001")
	if err := httpx.Serve("alex-admin", addr, adminapp.NewEngine()); err != nil {
		log.Fatalf("admin server: %v", err)
	}
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
