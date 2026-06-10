// Command server is the user-facing AlexMessage app (mirrors server.py).
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

	addr := listenAddr("0.0.0.0", "8765")
	log.Printf("[alexmessage] user app listening on %s", addr)

	//Start engine on addr
	if err := webapp.NewEngine().Run(addr); err != nil {
		log.Fatalf("server: %v", err)
	}
}

func listenAddr(defaultHost, defaultPort string) string {
	host := os.Getenv("HOST")
	if host == "" {
		host = defaultHost
	}
	port := os.Getenv("PORT")
	if port == "" {
		port = defaultPort
	}
	return host + ":" + port
}
