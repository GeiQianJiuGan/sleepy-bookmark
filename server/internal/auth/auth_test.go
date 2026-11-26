package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
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
			if _, err := a.Login(context.Background(), fmt.Sprintf("openid-%d", i), ""); err == nil {
				ok.Add(1)
			}
		}()
	}
	wg.Wait()
	if n, _ := a.db.UserCount(); n != 1 || ok.Load() != 1 {
		t.Fatalf("got %d users and %d successful logins, want 1 and 1", n, ok.Load())
	}
	if _, err := a.Login(context.Background(), "another-openid", ""); !errors.Is(err, ErrForbidden) {
		t.Fatalf("second openid login = %v, want forbidden", err)
	}
}

func TestOpenRegistrationAllowsMultipleAccounts(t *testing.T) {
	fakeWeChat(t)
	a := newAuth(t, config.Config{OpenRegistration: true})

	var ids []int64
	for _, openid := range []string{"alice", "bob", "carol"} {
		if _, err := a.Login(context.Background(), openid, ""); err != nil {
			t.Fatalf("login %q: %v", openid, err)
		}
		user, err := a.db.UserByOpenID(openid)
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, user.ID)
	}
	if ids[0] == ids[1] || ids[0] == ids[2] || ids[1] == ids[2] {
		t.Fatalf("account IDs are not distinct: %v", ids)
	}
}

func TestWhitelistOverridesOpenRegistration(t *testing.T) {
	fakeWeChat(t)
	a := newAuth(t, config.Config{
		OpenRegistration: true,
		OpenIDWhitelist:  []string{"allowed"},
	})
	if _, err := a.Login(context.Background(), "allowed", ""); err != nil {
		t.Fatalf("whitelisted login: %v", err)
	}
	if _, err := a.Login(context.Background(), "not-allowed", ""); !errors.Is(err, ErrForbidden) {
		t.Fatalf("unlisted login = %v, want forbidden", err)
	}
}

func TestTokenStopsWorkingWhenRemovedFromWhitelist(t *testing.T) {
	fakeWeChat(t)
	a := newAuth(t, config.Config{OpenIDWhitelist: []string{"alice", "bob"}})
	session, err := a.Login(context.Background(), "bob", "")
	if err != nil {
		t.Fatal(err)
	}
	token := session.Token
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

func TestRefreshTokenRenewsTheSessionAndNothingElse(t *testing.T) {
	fakeWeChat(t)
	a := newAuth(t, config.Config{OpenIDWhitelist: []string{"alice", "bob"}})
	session, err := a.Login(context.Background(), "bob", "")
	if err != nil {
		t.Fatal(err)
	}
	allowed := func(token string) bool {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		h := a.Middleware(func(c echo.Context) error { return nil })
		return h(echo.New().NewContext(req, httptest.NewRecorder())) == nil
	}
	if !allowed(session.Token) || allowed(session.RefreshToken) {
		t.Fatal("only the access token may call the API")
	}
	if _, err := a.Refresh(session.Token); err == nil {
		t.Fatal("an access token must not renew the session")
	}
	next, err := a.Refresh(session.RefreshToken)
	if err != nil || !allowed(next.Token) || next.RefreshToken == "" || next.ExpiresIn <= 0 {
		t.Fatalf("refresh: %+v, %v", next, err)
	}
	a.cfg.OpenIDWhitelist = []string{"alice"}
	if _, err := a.Refresh(next.RefreshToken); err == nil {
		t.Fatal("an account removed from the whitelist must not renew its session")
	}
}

func TestLoginExplainsWhyWeChatRefused(t *testing.T) {
	// This WeChat is busy the first time it sees a code, and otherwise refuses with the
	// errcode the code names.
	var calls atomic.Int32
	seen := map[string]bool{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		code := r.URL.Query().Get("js_code")
		if code == "busy-once" && !seen[code] {
			seen[code] = true
			json.NewEncoder(w).Encode(map[string]any{"errcode": -1, "errmsg": "system error"})
			return
		}
		if n, err := strconv.Atoi(code); err == nil {
			json.NewEncoder(w).Encode(map[string]any{"errcode": n, "errmsg": "refused, rid: 1-2-3"})
			return
		}
		json.NewEncoder(w).Encode(map[string]string{"openid": code})
	}))
	old := wxSessionURL
	wxSessionURL = srv.URL
	t.Cleanup(func() {
		wxSessionURL = old
		srv.Close()
	})
	a := newAuth(t, config.Config{})

	for _, tc := range []struct {
		code, appID string
		status      int
		want        string
		calls       int32
	}{
		{"40029", "", http.StatusBadRequest, "登录凭证无效", 1},
		{"40029", "wx-app", http.StatusBadRequest, "40029", 1},
		{"45011", "", http.StatusTooManyRequests, "太频繁", 1},
		{"12345", "", http.StatusBadGateway, "12345", 1},
		// Refused before WeChat is asked.
		{"real-code", "wx-other", http.StatusBadRequest, "wx-other", 0},
		{devCode, "", http.StatusBadRequest, "不在微信小程序内", 0},
		{mockCode, "wx-app", http.StatusBadRequest, "模拟登录凭证", 0},
		// What the client sent is not repeated when it could forge a log line.
		{"real-code", "wx-other\n2026/11/24 forged", http.StatusBadRequest, "无法识别", 0},
	} {
		calls.Store(0)
		_, err := a.Login(context.Background(), tc.code, tc.appID)
		var he *echo.HTTPError
		if !errors.As(err, &he) || he.Code != tc.status || !strings.Contains(fmt.Sprint(he.Message), tc.want) {
			t.Errorf("code %q, appid %q: got %v, want %d with %q", tc.code, tc.appID, err, tc.status, tc.want)
			continue
		}
		if strings.Contains(fmt.Sprint(he.Message), "wx-secret") {
			t.Errorf("code %q: message leaks the app secret: %v", tc.code, he.Message)
		}
		if strings.ContainsAny(he.Error(), "\r\n") {
			t.Errorf("code %q, appid %q: the logged error spans several lines: %q", tc.code, tc.appID, he.Error())
		}
		if calls.Load() != tc.calls {
			t.Errorf("code %q, appid %q: WeChat was asked %d times, want %d", tc.code, tc.appID, calls.Load(), tc.calls)
		}
	}

	calls.Store(0)
	if _, err := a.Login(context.Background(), "busy-once", "wx-app"); err != nil || calls.Load() != 2 {
		t.Fatalf("a busy WeChat should be asked once more: %v after %d calls", err, calls.Load())
	}
}
