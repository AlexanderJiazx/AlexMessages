// Command meet is the Alex Meet server: Google Meet-style multi-party meetings
// (standard WebRTC mesh or VolcEngine RTC) sharing the Alex Messages account
// database. The listen address comes from CALL_HOST/CALL_PORT.
package main

import (
	"log"
	"os"

	"github.com/gin-gonic/gin"

	"alexmessage/internal/auth"
	"alexmessage/internal/db"
	"alexmessage/internal/httpx"
	"alexmessage/internal/meet"
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

	addr := envOr("CALL_HOST", "127.0.0.1") + ":" + envOr("CALL_PORT", "8002")
	if err := httpx.Serve("alex-meet", addr, meet.NewEngine()); err != nil {
		log.Fatalf("meet server: %v", err)
	}
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
