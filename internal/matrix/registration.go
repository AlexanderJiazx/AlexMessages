package matrix

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
)

// RegistrationYAML renders the appservice registration file a homeserver
// admin installs (synapse app_service_config_files / conduit config). Tokens
// are read from env when present, else generated fresh — either way the same
// values must be exported as MATRIX_AS_TOKEN / MATRIX_HS_TOKEN on the server.
func RegistrationYAML(cfg *Config, asToken, hsToken, baseURL string) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "id: alex-messages\n")
	fmt.Fprintf(&sb, "url: %s\n", baseURL)
	fmt.Fprintf(&sb, "as_token: %s\n", asToken)
	fmt.Fprintf(&sb, "hs_token: %s\n", hsToken)
	fmt.Fprintf(&sb, "sender_localpart: %s\n", cfg.BotLocalpart)
	fmt.Fprintf(&sb, "rate_limited: false\n")
	fmt.Fprintf(&sb, "push_ephemeral: true\n")
	fmt.Fprintf(&sb, "namespaces:\n")
	fmt.Fprintf(&sb, "  users:\n")
	fmt.Fprintf(&sb, "    - exclusive: true\n")
	fmt.Fprintf(&sb, "      regex: '@%s.*:%s'\n", regexEscape(cfg.UserPrefix), regexEscape(cfg.ServerName))
	fmt.Fprintf(&sb, "  aliases: []\n")
	fmt.Fprintf(&sb, "  rooms: []\n")
	return sb.String()
}

// GenerateToken returns a fresh random hex token for registration files.
func GenerateToken() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return ""
	}
	return hex.EncodeToString(b)
}

// regexEscape quotes characters that matter inside a YAML single-quoted
// regex value. Server names and prefixes are dot/underscore heavy.
func regexEscape(s string) string {
	var sb strings.Builder
	for _, r := range s {
		if strings.ContainsRune(`\.^$|()[]{}*+?`, r) {
			sb.WriteByte('\\')
		}
		sb.WriteRune(r)
	}
	return sb.String()
}
