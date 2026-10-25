package adminapp

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"alexmessage/internal/auth"
	"alexmessage/internal/db"
	"alexmessage/internal/ota/census"
)

func TestOTAInstallsAndCensusCleanup(t *testing.T) {
	gin.SetMode(gin.TestMode)
	reviewDatabase(t)
	store := t.TempDir()
	t.Setenv("OTA_DIR", store)
	adminID, err := db.CreateUser("admin", "hash", "Admin", "approved", true)
	if err != nil {
		t.Fatal(err)
	}
	token, _, err := auth.IssueSession(adminID, "admin")
	if err != nil {
		t.Fatal(err)
	}
	bobID, err := db.CreateUser("bob", "hash", "Bob", "approved", false)
	if err != nil {
		t.Fatal(err)
	}
	r := NewEngine()
	get := func(cookie bool) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/api/ota/installs", nil)
		if cookie {
			req.AddCookie(&http.Cookie{Name: auth.AdminCookie, Value: token})
		}
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w
	}
	type listing struct {
		Installs []census.Install `json:"installs"`
		MaxRows  int              `json:"max_rows"`
	}

	if w := get(false); w.Code != http.StatusUnauthorized {
		t.Fatalf("without admin login: %d", w.Code)
	}
	// No census yet: an empty list, and no file is created.
	w := get(true)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"installs":[]`) {
		t.Fatalf("empty census: %d %s", w.Code, w.Body.String())
	}
	if c, err := census.Shared(filepath.Join(store, census.FileName), false); err != nil || c != nil {
		t.Fatalf("listing created the census: %v %v", c, err)
	}

	cdb, err := census.Shared(filepath.Join(store, census.FileName), true)
	if err != nil {
		t.Fatal(err)
	}
	registered := func() (bool, error) { return true, nil }
	for i, rep := range []census.Report{
		{ClientID: "c-bob", UserID: int64(bobID)},
		{ClientID: "c-anon", FatalError: `<script>alert(1)</script>`},
	} {
		rep.Platform, rep.Runtime, rep.EmbeddedUpdateID, rep.Channel = "android", "a1b2c3", "e", "production"
		if _, err := cdb.Record(rep, registered, time.Unix(1700000000+int64(i), 0)); err != nil {
			t.Fatal(err)
		}
	}

	w = get(true)
	var got listing
	if w.Code != http.StatusOK || w.Header().Get("Cache-Control") != "no-store" || json.Unmarshal(w.Body.Bytes(), &got) != nil ||
		len(got.Installs) != 2 || got.MaxRows != census.MaxRows || got.Installs[0].ClientID != "c-anon" {
		t.Fatalf("installs: %d %s", w.Code, w.Body.String())
	}
	// Phone-supplied text reaches the admin page escaped.
	if strings.Contains(w.Body.String(), "<script>") || !strings.Contains(w.Body.String(), "u003cscript") {
		t.Fatalf("fatal error not escaped: %s", w.Body.String())
	}

	// Deleting the user removes the rows that claim them.
	req := httptest.NewRequest(http.MethodDelete, "/api/users/"+strconv.Itoa(bobID), nil)
	req.AddCookie(&http.Cookie{Name: auth.AdminCookie, Value: token})
	del := httptest.NewRecorder()
	r.ServeHTTP(del, req)
	if del.Code != http.StatusOK {
		t.Fatalf("delete: %d %s", del.Code, del.Body.String())
	}
	rows, err := cdb.List()
	if err != nil || len(rows) != 1 || rows[0].ClientID != "c-anon" {
		t.Fatalf("after delete %+v, %v", rows, err)
	}
}
