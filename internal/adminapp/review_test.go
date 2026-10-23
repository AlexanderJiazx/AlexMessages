package adminapp

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"alexmessage/internal/auth"
	"alexmessage/internal/db"
)

func reviewDatabase(t *testing.T) {
	t.Helper()
	root := t.TempDir()
	db.BaseDir = root
	db.DataDir = filepath.Join(root, "data")
	db.DBPath = filepath.Join(db.DataDir, "alexmessage.db")
	db.UploadRoot = filepath.Join(db.DataDir, "uploads")
	db.AvatarRoot = filepath.Join(db.DataDir, "avatars")
	if err := db.InitDB(); err != nil {
		t.Fatal(err)
	}
}

func TestRejectAndDisableLastAdmin(t *testing.T) {
	gin.SetMode(gin.TestMode)
	reviewDatabase(t)
	id, err := db.CreateUser("admin", "hash", "Admin", "approved", true)
	if err != nil {
		t.Fatal(err)
	}
	token, _, err := auth.IssueSession(id, "admin")
	if err != nil {
		t.Fatal(err)
	}
	r := NewEngine()
	for _, action := range []string{"reject", "disable"} {
		req := httptest.NewRequest("POST", "/api/users/"+strconv.Itoa(id)+"/"+action, nil)
		req.AddCookie(&http.Cookie{Name: auth.AdminCookie, Value: token})
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != 400 || auth.ResolveSession(token, "admin") == nil {
			t.Fatalf("%s status=%d body=%s", action, w.Code, w.Body.String())
		}
	}
}

func TestAdminServesFontAndIconAssets(t *testing.T) {
	gin.SetMode(gin.TestMode)
	reviewDatabase(t)
	for _, path := range []string{"fonts/fonts.css", "static/icons/favicon.svg"} {
		full := filepath.Join(db.BaseDir, path)
		if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte("asset"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	r := NewEngine()
	for _, path := range []string{"/fonts/fonts.css", "/static/icons/favicon.svg"} {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != 200 || w.Body.String() != "asset" {
			t.Fatalf("%s status=%d", path, w.Code)
		}
	}
	// The admin host is public: its mounts must never list their files.
	for _, path := range []string{"/static/", "/fonts/", "/static/icons/"} {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != 404 {
			t.Fatalf("%s status=%d body=%s", path, w.Code, w.Body.String())
		}
	}
}

// Only approved admins count toward the last administrator. A disabled admin
// can be deleted while another approved admin remains; the last approved
// admin cannot be deleted.
func TestDeleteDisabledAdmin(t *testing.T) {
	gin.SetMode(gin.TestMode)
	reviewDatabase(t)
	a, err := db.CreateUser("admin", "hash", "Admin", "approved", true)
	if err != nil {
		t.Fatal(err)
	}
	b, err := db.CreateUser("admin2", "hash", "Admin2", "approved", true)
	if err != nil {
		t.Fatal(err)
	}
	token, _, err := auth.IssueSession(a, "admin")
	if err != nil {
		t.Fatal(err)
	}
	r := NewEngine()
	do := func(method, path string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, nil)
		req.AddCookie(&http.Cookie{Name: auth.AdminCookie, Value: token})
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w
	}
	if w := do("POST", "/api/users/"+strconv.Itoa(b)+"/disable"); w.Code != 200 {
		t.Fatalf("disable status=%d body=%s", w.Code, w.Body.String())
	}
	if w := do("DELETE", "/api/users/"+strconv.Itoa(b)); w.Code != 200 {
		t.Fatalf("delete disabled admin status=%d body=%s", w.Code, w.Body.String())
	}
	if u, _ := db.GetUserByID(b); u != nil {
		t.Fatal("disabled admin still exists")
	}
	w := do("DELETE", "/api/users/"+strconv.Itoa(a))
	if w.Code != 400 || !strings.Contains(w.Body.String(), "Refusing to delete the last administrator") {
		t.Fatalf("delete last admin status=%d body=%s", w.Code, w.Body.String())
	}
	if u, _ := db.GetUserByID(a); u == nil {
		t.Fatal("last admin deleted")
	}
}
