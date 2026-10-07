// matrix-registration prints the appservice registration.yaml a homeserver
// admin needs to attach Alex Messages as a Matrix Application Service.
//
// Usage:
//
//	MATRIX_SERVER_NAME=matrix.example.com \
//	MATRIX_AS_URL=http://alex-messages.internal:8080 \
//	  go run ./cmd/matrix-registration > registration.yaml
//
// MATRIX_AS_TOKEN / MATRIX_HS_TOKEN are embedded when already generated; if
// unset, fresh tokens are generated and printed — export the same values on
// the Alex Messages server. Never commit the generated file.
package main

import (
	"flag"
	"fmt"
	"os"

	"alexmessage/internal/matrix"
)

func main() {
	baseURL := flag.String("url", "", "base URL the homeserver reaches this server at (required)")
	serverName := flag.String("server-name", "", "Matrix server_name (default: MATRIX_SERVER_NAME env)")
	prefix := flag.String("user-prefix", "", "bridged user prefix (default: MATRIX_USER_PREFIX or am_)")
	bot := flag.String("bot", "", "bot localpart (default: MATRIX_BOT_LOCALPART or alexmessages)")
	flag.Parse()

	if *baseURL == "" {
		*baseURL = os.Getenv("MATRIX_AS_URL")
	}
	if *baseURL == "" {
		fmt.Fprintln(os.Stderr, "missing --url (or MATRIX_AS_URL): the URL the homeserver posts to")
		os.Exit(2)
	}
	cfg := &matrix.Config{
		ServerName:   *serverName,
		UserPrefix:   *prefix,
		BotLocalpart: *bot,
	}
	if cfg.ServerName == "" {
		cfg.ServerName = os.Getenv("MATRIX_SERVER_NAME")
	}
	if cfg.ServerName == "" {
		fmt.Fprintln(os.Stderr, "missing --server-name (or MATRIX_SERVER_NAME)")
		os.Exit(2)
	}
	if cfg.UserPrefix == "" {
		cfg.UserPrefix = "am_"
	}
	if cfg.BotLocalpart == "" {
		cfg.BotLocalpart = "alexmessages"
	}

	asToken := os.Getenv("MATRIX_AS_TOKEN")
	hsToken := os.Getenv("MATRIX_HS_TOKEN")
	if asToken == "" {
		asToken = matrix.GenerateToken()
		fmt.Fprintf(os.Stderr, "generated MATRIX_AS_TOKEN=%s\n", asToken)
	}
	if hsToken == "" {
		hsToken = matrix.GenerateToken()
		fmt.Fprintf(os.Stderr, "generated MATRIX_HS_TOKEN=%s\n", hsToken)
	}
	fmt.Print(matrix.RegistrationYAML(cfg, asToken, hsToken, *baseURL))
}
