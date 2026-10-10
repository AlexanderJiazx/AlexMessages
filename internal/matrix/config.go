// Package matrix implements the Matrix Application Service bridge: it lets
// Alex Messages users exchange direct messages with Matrix users in both
// directions without Alex Messages becoming a homeserver. When the MATRIX_*
// env vars are unset the bridge is fully disabled and no code path changes.
package matrix

import (
	"fmt"
	"os"
	"strings"
)

// Config is the bridge configuration, sourced entirely from env vars.
type Config struct {
	// HomeserverURL is the base URL of the attached homeserver,
	// e.g. "http://localhost:8008".
	HomeserverURL string
	// ServerName is the homeserver's server_name, e.g. "matrix.example.com".
	// It is the domain half of every MXID the bridge produces.
	ServerName string
	// ASToken authenticates the bridge -> homeserver requests.
	ASToken string
	// HSToken authenticates homeserver -> bridge requests.
	HSToken string
	// UserPrefix prefixes local usernames inside the bridged namespace
	// ("am_" → "@am_bob:server"). Default "am_".
	UserPrefix string
	// BotLocalpart is the appservice bot's localpart. Default "alexmessages".
	BotLocalpart string
}

// LoadConfig reads the MATRIX_* env vars. It returns (nil, nil) when the
// bridge is disabled (no vars set) and an error when partially configured.
func LoadConfig() (*Config, error) {
	c := &Config{
		HomeserverURL: strings.TrimRight(os.Getenv("MATRIX_HOMESERVER_URL"), "/"),
		ServerName:    os.Getenv("MATRIX_SERVER_NAME"),
		ASToken:       os.Getenv("MATRIX_AS_TOKEN"),
		HSToken:       os.Getenv("MATRIX_HS_TOKEN"),
		UserPrefix:    envOr("MATRIX_USER_PREFIX", "am_"),
		BotLocalpart:  envOr("MATRIX_BOT_LOCALPART", "alexmessages"),
	}
	required := []string{c.HomeserverURL, c.ServerName, c.ASToken, c.HSToken}
	set := 0
	for _, v := range required {
		if v != "" {
			set++
		}
	}
	if set == 0 {
		return nil, nil
	}
	if set != len(required) {
		return nil, fmt.Errorf(
			"partially configured Matrix bridge: MATRIX_HOMESERVER_URL, MATRIX_SERVER_NAME, " +
				"MATRIX_AS_TOKEN and MATRIX_HS_TOKEN must all be set (or all unset)")
	}
	return c, nil
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// ParseMXID splits "@localpart:server.name" into its halves. Both halves must
// be non-empty and the localpart must not contain '@' or ':' beyond the
// delimiters. Port suffixes on the server are accepted ("server:8448").
func ParseMXID(mxid string) (localpart, server string, ok bool) {
	if !strings.HasPrefix(mxid, "@") {
		return "", "", false
	}
	rest := mxid[1:]
	i := strings.Index(rest, ":")
	if i <= 0 || i == len(rest)-1 {
		return "", "", false
	}
	localpart, server = rest[:i], rest[i+1:]
	if localpart == "" || server == "" {
		return "", "", false
	}
	return localpart, server, true
}

// PuppetMXID returns the Matrix user id that puppets a local account.
func (c *Config) PuppetMXID(username string) string {
	return "@" + c.UserPrefix + username + ":" + c.ServerName
}

// BotMXID returns the appservice bot's Matrix user id.
func (c *Config) BotMXID() string {
	return "@" + c.BotLocalpart + ":" + c.ServerName
}

// UsernameForPuppet maps "@am_bob:<our server>" back to local username "bob".
// ok is false for mxids outside our bridged namespace or on other servers.
func (c *Config) UsernameForPuppet(mxid string) (string, bool) {
	lp, server, ok := ParseMXID(mxid)
	if !ok || server != c.ServerName {
		return "", false
	}
	if !strings.HasPrefix(lp, c.UserPrefix) {
		return "", false
	}
	name := strings.TrimPrefix(lp, c.UserPrefix)
	return name, name != ""
}

// OwnMXID reports whether mxid belongs to the bridge (bot or any puppet).
// Events sent by our own puppets come back through transactions and must be
// ignored to avoid echoing outbound traffic into new local messages.
func (c *Config) OwnMXID(mxid string) bool {
	if mxid == c.BotMXID() {
		return true
	}
	_, ok := c.UsernameForPuppet(mxid)
	return ok
}
