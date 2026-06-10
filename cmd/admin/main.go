// Command admin is the AlexMessage admin control panel (mirrors admin.py).
//
// Runs on its own port (default 8001) sharing the user app's database. Listen
// address comes from ADMIN_HOST/ADMIN_PORT.
package main

import (
	"log"
	"os"

	"github.com/gin-gonic/gin"

	"alexmessage/internal/adminapp"
	"alexmessage/internal/auth"
	"alexmessage/internal/db"
)

func main() {
	gin.SetMode(gin.ReleaseMode)

	if err := db.InitDB(); err != nil {
		log.Fatalf("db init: %v", err)
	}
	if err := auth.BootstrapAdmin(); err != nil {
		log.Fatalf("admin bootstrap: %v", err)
	}

	host := envOr("ADMIN_HOST", "127.0.0.1")
	port := envOr("ADMIN_PORT", "8001")
	addr := host + ":" + port
	log.Printf("[alexmessage] admin panel listening on %s", addr)
	if err := adminapp.NewEngine().Run(addr); err != nil {
		log.Fatalf("admin server: %v", err)
	}
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
