// Command voicecall is the AlexMessage WebRTC signaling server (mirrors
// voicecall.py). It runs on its own port sharing the user app's database.
// Listen address comes from CALL_HOST/CALL_PORT.
package main

import (
	"log"
	"os"

	"github.com/gin-gonic/gin"

	"alexmessage/internal/auth"
	"alexmessage/internal/db"
	"alexmessage/internal/voicecall"
)

func main() {
	gin.SetMode(gin.ReleaseMode)

	if err := db.InitDB(); err != nil {
		log.Fatalf("db init: %v", err)
	}
	if err := auth.BootstrapAdmin(); err != nil {
		log.Fatalf("admin bootstrap: %v", err)
	}

	host := envOr("CALL_HOST", "127.0.0.1")
	port := envOr("CALL_PORT", "8002")
	addr := host + ":" + port
	log.Printf("[alexmessage] voice-call server listening on %s", addr)
	if err := voicecall.NewEngine().Run(addr); err != nil {
		log.Fatalf("voicecall server: %v", err)
	}
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
