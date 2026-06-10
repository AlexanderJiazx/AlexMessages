package webapp

import (
	"strings"

	"github.com/gin-gonic/gin"
)

// bindJSON decodes the request body into a generic object, like FastAPI's
// `payload: dict`. Invalid/empty bodies yield an empty map (handlers then fall
// back to their per-field defaults).
func bindJSON(c *gin.Context) map[string]any {
	var m map[string]any
	_ = c.ShouldBindJSON(&m)
	if m == nil {
		m = map[string]any{}
	}
	return m
}

// strField returns m[key] when it's a string, else "" (mirrors dict.get(k, "")).
func strField(m map[string]any, key string) string {
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}

// isStr reports whether m[key] is a JSON string (Python's isinstance(x, str)).
func isStr(m map[string]any, key string) (string, bool) {
	v, ok := m[key].(string)
	return v, ok
}

// truncateRunes slices a string to at most n Unicode code points, matching
// Python's `s[:n]` semantics (which the original used for length caps).
func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

// firstNonEmpty mirrors Python's `a or b` for strings.
func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// trimSpace is Python's str.strip().
func trimSpace(s string) string { return strings.TrimSpace(s) }
