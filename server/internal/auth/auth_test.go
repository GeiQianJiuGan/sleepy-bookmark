package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/labstack/echo/v4"

	"github.com/GeiQianJiuGan/sleepy-bookmark/server/internal/config"
	"github.com/GeiQianJiuGan/sleepy-bookmark/server/internal/store"
)

func TestCode2SessionErrorHidesSecret(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	srv.Close() // connection refused
	old := wxSessionURL
	wxSessionURL = srv.URL
	defer func() { wxSessionURL = old }()

	a := &Auth{cfg: config.Config{WXAppID: "wx-app", WXSecret: "top-secret-value"}}
	_, err := a.code2Session(context.Background(), "code")
	if err == nil {
		t.Fatal("expected an error")
	}
	if strings.Contains(err.Error(), "top-secret-value") || strings.Contains(err.Error(), "secret=") {
		t.Fatalf("error leaks the app secret: %v", err)
	}
}

// fakeWeChat answers jscode2session with the code as the openid.
func fakeWeChat(t *testing.T) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{"openid": r.URL.Query().Get("js_code")})
	}))
	old := wxSessionURL
	wxSessionURL = srv.URL
	t.Cleanup(func() {
		wxSessionURL = old
		srv.Close()
	})
}

func newAuth(t *testing.T, cfg config.Config) *Auth {
	t.Helper()
	cfg.DataDir = t.TempDir()
	cfg.WXAppID, cfg.WXSecret = "wx-app", "wx-secret"
	db, err := store.Open(filepath.Join(cfg.DataDir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	a, err := New(cfg, db)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func TestOnlyFirstAccountRegistersWithoutWhitelist(t *testing.T) {
	fakeWeChat(t)
	a := newAuth(t, config.Config{})

	var wg sync.WaitGroup
	var ok atomic.Int32
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := a.Login(context.Background(), fmt.Sprintf("openid-%d", i)); err == nil {
				ok.Add(1)
			}
		}()
	}
	wg.Wait()
	if n, _ := a.db.UserCount(); n != 1 || ok.Load() != 1 {
		t.Fatalf("got %d users and %d successful logins, want 1 and 1", n, ok.Load())
	}
}

func TestTokenStopsWorkingWhenRemovedFromWhitelist(t *testing.T) {
	fakeWeChat(t)
	a := newAuth(t, config.Config{OpenIDWhitelist: []string{"alice", "bob"}})
	token, err := a.Login(context.Background(), "bob")
	if err != nil {
		t.Fatal(err)
	}
	status := func() int {
		e := echo.New()
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		rec := httptest.NewRecorder()
		h := a.Middleware(func(c echo.Context) error { return c.NoContent(http.StatusNoContent) })
		if err := h(e.NewContext(req, rec)); err != nil {
			return err.(*echo.HTTPError).Code
		}
		return rec.Code
	}
	if got := status(); got != http.StatusNoContent {
		t.Fatalf("whitelisted account: got %d", got)
	}
	a.cfg.OpenIDWhitelist = []string{"alice"}
	if got := status(); got != http.StatusUnauthorized {
		t.Fatalf("removed account: got %d, want 401", got)
	}
}
