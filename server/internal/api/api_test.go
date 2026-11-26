package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log"
	"maps"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/GeiQianJiuGan/sleepy-bookmark/server/internal/api"
	"github.com/GeiQianJiuGan/sleepy-bookmark/server/internal/auth"
	"github.com/GeiQianJiuGan/sleepy-bookmark/server/internal/cache"
	"github.com/GeiQianJiuGan/sleepy-bookmark/server/internal/config"
	"github.com/GeiQianJiuGan/sleepy-bookmark/server/internal/demo"
	"github.com/GeiQianJiuGan/sleepy-bookmark/server/internal/library"
	"github.com/GeiQianJiuGan/sleepy-bookmark/server/internal/source"
	"github.com/GeiQianJiuGan/sleepy-bookmark/server/internal/store"
	"github.com/golang-jwt/jwt/v5"
)

const testJWTSecret = "api-test-secret"

type client struct {
	t     *testing.T
	base  string
	token string
	db    *store.DB
	// site is the demo site that the seeded sources point at.
	site *httptest.Server
	lib  *library.Library
	dir  string
	// gone is a path of the demo site that answers 404, if set.
	gone atomic.Pointer[string]
}

func (c *client) do(method, path string, body any, out any) int {
	c.t.Helper()
	var r io.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		r = bytes.NewReader(raw)
	}
	req, _ := http.NewRequest(method, c.base+path, r)
	req.Header.Set("Content-Type", "application/json")
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if out != nil && resp.StatusCode < 300 {
		if err := json.Unmarshal(raw, out); err != nil {
			c.t.Fatalf("%s %s: %v\n%s", method, path, err, raw)
		}
	}
	return resp.StatusCode
}

func TestMain(m *testing.M) {
	source.RetryDelays = []time.Duration{time.Millisecond, time.Millisecond}
	api.SetLocalLists(true)
	os.Exit(m.Run())
}

// login starts a dev-login session and returns the client.
func login(t *testing.T) *client {
	t.Helper()
	c := newClient(t, config.Config{DevLogin: true})
	var out struct{ Token string }
	c.do("POST", "/api/auth/login", map[string]string{"code": "x"}, &out)
	c.token = out.Token
	return c
}

type sourceInfo struct {
	ID     int64
	Name   string
	Rule   map[string]any
	Health *struct {
		Failures    int
		LastError   string
		PausedUntil int64
	}
}

func (c *client) sources(query string) []sourceInfo {
	c.t.Helper()
	var list []sourceInfo
	c.do("GET", "/api/sources"+query, nil, &list)
	return list
}

// addCopy saves the demo novel source again under another name and base URL.
func (c *client) addCopy(name, baseURL string) {
	c.t.Helper()
	rule := c.sources("?type=novel")[0].Rule
	rule["name"] = name
	if baseURL != "" {
		rule["baseUrl"] = baseURL
	}
	if code := c.do("POST", "/api/sources", rule, nil); code >= 300 {
		c.t.Fatalf("create source %q: %d", name, code)
	}
}

type searchResult struct {
	Books []struct {
		ID         int64
		Title      string
		SourceName string
	}
	Errors []struct{ Source, Error, Kind string }
}

const qStar = "/api/search?type=novel&q=%E6%98%9F%E6%B2%B3" // 星河

func TestDeadSourceIsPausedAndDoesNotBreakSearch(t *testing.T) {
	c := login(t)
	c.addCopy("dead", "http://127.0.0.1:1")

	for i := 0; i < 3; i++ {
		var r searchResult
		c.do("GET", qStar, nil, &r)
		if len(r.Books) != 1 || len(r.Errors) != 1 || r.Errors[0].Kind != "network" {
			t.Fatalf("search %d: %+v", i, r)
		}
		if strings.Contains(r.Errors[0].Error, "127.0.0.1") {
			t.Fatalf("error message shows the URL: %q", r.Errors[0].Error)
		}
	}
	var r searchResult
	c.do("GET", qStar, nil, &r)
	if len(r.Books) != 1 || len(r.Errors) != 1 || r.Errors[0].Kind != "paused" {
		t.Fatalf("search after three failures: %+v", r)
	}

	var dead sourceInfo
	for _, s := range c.sources("") {
		if s.Name == "dead" {
			dead = s
		} else if s.Health != nil {
			t.Errorf("working source %q has health %+v", s.Name, s.Health)
		}
	}
	if dead.Health == nil || dead.Health.Failures != 3 || dead.Health.PausedUntil == 0 {
		t.Fatalf("dead source health: %+v", dead.Health)
	}

	// Saving the source again gives it a fresh chance.
	c.do("PUT", "/api/sources/"+itoa(dead.ID)+"/enabled", map[string]bool{"enabled": true}, nil)
	c.do("GET", qStar, nil, &r)
	if len(r.Errors) != 1 || r.Errors[0].Kind != "network" {
		t.Fatalf("search after reset: %+v", r.Errors)
	}
}

func TestCatalogSurvivesASourceOutage(t *testing.T) {
	c := login(t)
	var r searchResult
	c.do("GET", qStar, nil, &r)
	path := "/api/books/" + itoa(r.Books[0].ID) + "/chapters"
	var chapters []struct{ ID int64 }
	c.do("GET", path, nil, &chapters)
	if len(chapters) != 30 {
		t.Fatalf("chapters: %d", len(chapters))
	}

	// A rule edit expires the stored catalog, and then the site goes down.
	src := c.sources("?type=novel")[0]
	src.Rule["headers"] = map[string]string{"X-Changed": "1"}
	if code := c.do("PUT", "/api/sources/"+itoa(src.ID), src.Rule, nil); code != http.StatusNoContent {
		t.Fatalf("update: %d", code)
	}
	c.site.Close()
	// The book still opens with the catalog read earlier...
	chapters = nil
	if code := c.do("GET", path, nil, &chapters); code != 200 || len(chapters) != 30 {
		t.Fatalf("catalog during outage: %d, %d chapters", code, len(chapters))
	}
	// ...and an explicit refresh says what is wrong, with a code the client can act on.
	req, _ := http.NewRequest("GET", c.base+path+"?refresh=1", nil)
	req.Header.Set("Authorization", "Bearer "+c.token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var fail struct{ Message, Code string }
	json.NewDecoder(resp.Body).Decode(&fail)
	if resp.StatusCode != http.StatusBadGateway || fail.Code != "network" || fail.Message == "" || strings.Contains(fail.Message, "127.0.0.1") {
		t.Fatalf("refresh during outage: %d %+v", resp.StatusCode, fail)
	}
}

func TestSwitchSourceMovesShelfAndProgress(t *testing.T) {
	c := login(t)
	c.addCopy("mirror", "")
	var r searchResult
	c.do("GET", qStar, nil, &r)
	if len(r.Books) != 2 {
		t.Fatalf("search: %+v", r)
	}
	from := r.Books[0].ID

	var chapters []struct {
		ID    int64
		Title string
	}
	c.do("GET", "/api/books/"+itoa(from)+"/chapters", nil, &chapters)
	c.do("PUT", "/api/favorites/"+itoa(from), nil, nil)
	c.do("PUT", "/api/history", map[string]any{"chapterId": chapters[6].ID, "position": 120}, nil)

	var alts []struct {
		ID         int64
		SourceName string
	}
	c.do("GET", "/api/books/"+itoa(from)+"/alternatives", nil, &alts)
	if len(alts) != 1 || alts[0].ID == from || alts[0].SourceName != "mirror" {
		t.Fatalf("alternatives: %+v", alts)
	}
	to := alts[0].ID
	if code := c.do("POST", "/api/books/"+itoa(from)+"/switch", map[string]int64{"to": to}, nil); code != http.StatusNoContent {
		t.Fatalf("switch: %d", code)
	}

	var shelf []struct {
		ID       int64
		Progress *struct {
			ChapterTitle string
			Position     int
		}
	}
	c.do("GET", "/api/favorites?type=novel", nil, &shelf)
	if len(shelf) != 1 || shelf[0].ID != to || shelf[0].Progress == nil ||
		shelf[0].Progress.ChapterTitle != chapters[6].Title || shelf[0].Progress.Position != 0 {
		t.Fatalf("shelf after switch: %+v", shelf)
	}
	c.do("GET", "/api/history?type=novel", nil, &shelf)
	if len(shelf) != 1 || shelf[0].ID != to {
		t.Fatalf("history after switch: %+v", shelf)
	}
}

func newClient(t *testing.T, cfg config.Config) *client {
	t.Helper()
	c := &client{t: t}
	pages := demo.Handler()
	site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if gone := c.gone.Load(); gone != nil && r.URL.Path == *gone {
			http.NotFound(w, r)
			return
		}
		pages.ServeHTTP(w, r)
	}))
	t.Cleanup(site.Close)

	cfg.DataDir = t.TempDir()
	if cfg.JWTSecret == "" {
		cfg.JWTSecret = testJWTSecret
	}
	db, err := store.Open(filepath.Join(cfg.DataDir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	a, err := auth.New(cfg, db)
	if err != nil {
		t.Fatal(err)
	}
	disk := cache.New(cfg.DataDir)
	lib := library.New(cfg, db, disk)
	for _, src := range demo.Sources(site.URL) {
		src.RateLimitMs = 1
		if err := lib.SeedSource(src); err != nil {
			t.Fatal(err)
		}
	}
	srv := httptest.NewServer(api.New(db, a, lib, disk))
	t.Cleanup(srv.Close)
	c.base, c.site, c.lib, c.dir, c.db = srv.URL, site, lib, cfg.DataDir, db
	return c
}

func withUser(c *client, openid string) *client {
	c.t.Helper()
	user, err := c.db.CreateUser(openid)
	if err != nil {
		c.t.Fatal(err)
	}
	claims := jwt.RegisteredClaims{
		Subject:   strconv.FormatInt(user.ID, 10),
		Audience:  jwt.ClaimStrings{"access"},
		ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
	}
	token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(testJWTSecret))
	if err != nil {
		c.t.Fatal(err)
	}
	return &client{
		t: c.t, base: c.base, token: token, site: c.site, lib: c.lib, dir: c.dir, db: c.db,
	}
}

func TestUserIsolationAndDefaultAdmin(t *testing.T) {
	base := newClient(t, config.Config{})
	owner := withUser(base, "owner")
	friend := withUser(base, "friend")

	var found searchResult
	if code := owner.do("GET", qStar, nil, &found); code != http.StatusOK || len(found.Books) == 0 {
		t.Fatalf("search: status %d, result %+v", code, found)
	}
	bookID := found.Books[0].ID
	var chapters []struct{ ID int64 }
	if code := owner.do("GET", "/api/books/"+itoa(bookID)+"/chapters", nil, &chapters); code != http.StatusOK || len(chapters) == 0 {
		t.Fatalf("chapters: status %d, count %d", code, len(chapters))
	}
	if code := owner.do("PUT", "/api/favorites/"+itoa(bookID), nil, nil); code != http.StatusNoContent {
		t.Fatalf("add favorite: %d", code)
	}
	if code := owner.do("PUT", "/api/history", map[string]any{"chapterId": chapters[0].ID, "position": 42}, nil); code != http.StatusNoContent {
		t.Fatalf("save history: %d", code)
	}
	var favorites, history []struct{ ID int64 }
	if code := friend.do("GET", "/api/favorites", nil, &favorites); code != http.StatusOK || len(favorites) != 0 {
		t.Fatalf("friend favorites: status %d, list %+v", code, favorites)
	}
	if code := friend.do("GET", "/api/history", nil, &history); code != http.StatusOK || len(history) != 0 {
		t.Fatalf("friend history: status %d, list %+v", code, history)
	}

	var ownerMe, friendMe struct {
		ID    int64
		Admin bool
	}
	owner.do("GET", "/api/me", nil, &ownerMe)
	friend.do("GET", "/api/me", nil, &friendMe)
	if ownerMe.ID == 0 || !ownerMe.Admin || friendMe.ID == 0 || friendMe.Admin {
		t.Fatalf("me owner=%+v friend=%+v", ownerMe, friendMe)
	}
	var users []struct {
		ID        int64
		OpenID    string
		CreatedAt int64
		Admin     bool
		Favorites int
		History   int
	}
	if code := friend.do("GET", "/api/users", nil, &users); code != http.StatusForbidden {
		t.Fatalf("friend users: %d, want 403", code)
	}
	if code := owner.do("GET", "/api/users", nil, &users); code != http.StatusOK || len(users) != 2 {
		t.Fatalf("owner users: status %d, list %+v", code, users)
	}
	if users[0].OpenID != "owner" || !users[0].Admin || users[0].Favorites != 1 || users[0].History != 1 ||
		users[1].OpenID != "friend" || users[1].Admin || users[1].Favorites != 0 || users[1].History != 0 {
		t.Fatalf("user summaries: %+v", users)
	}

	var ownerTask struct{ ID int64 }
	if code := owner.do("POST", "/api/books/"+itoa(bookID)+"/cache", nil, &ownerTask); code != http.StatusOK || ownerTask.ID == 0 {
		t.Fatalf("queue owner cache: status %d, task %+v", code, ownerTask)
	}
	var tasks []struct{ ID int64 }
	if code := friend.do("GET", "/api/tasks", nil, &tasks); code != http.StatusOK || len(tasks) != 0 {
		t.Fatalf("friend tasks before joining: status %d, tasks %+v", code, tasks)
	}
	var friendTask struct{ ID int64 }
	if code := friend.do("POST", "/api/books/"+itoa(bookID)+"/cache", nil, &friendTask); code != http.StatusOK || friendTask.ID != ownerTask.ID {
		t.Fatalf("join cache task: status %d, task %+v, owner task %+v", code, friendTask, ownerTask)
	}
	if code := friend.do("GET", "/api/tasks", nil, &tasks); code != http.StatusOK || len(tasks) != 1 || tasks[0].ID != ownerTask.ID {
		t.Fatalf("friend tasks after joining: status %d, tasks %+v", code, tasks)
	}

	var sourceList []sourceInfo
	owner.do("GET", "/api/sources?type=novel", nil, &sourceList)
	rule := sourceList[0].Rule
	rule["name"] = "owner-added-source"
	if code := friend.do("POST", "/api/sources", rule, nil); code != http.StatusForbidden {
		t.Fatalf("friend create source: %d, want 403", code)
	}
	if code := owner.do("POST", "/api/sources", rule, nil); code != http.StatusOK {
		t.Fatalf("owner create source: %d, want 200", code)
	}
	subID, err := base.db.CreateSubscription("test", "https://example.invalid/list.json")
	if err != nil {
		t.Fatal(err)
	}
	subPath := "/api/subscriptions/" + itoa(subID)
	if code := friend.do("DELETE", subPath, nil, nil); code != http.StatusForbidden {
		t.Fatalf("friend delete subscription: %d, want 403", code)
	}
	if code := owner.do("DELETE", subPath, nil, nil); code != http.StatusNoContent {
		t.Fatalf("owner delete subscription: %d, want 204", code)
	}
}

func TestConfiguredAdminOpenIDsOverrideEarliestUser(t *testing.T) {
	base := newClient(t, config.Config{AdminOpenIDs: []string{"friend"}})
	owner := withUser(base, "owner")
	friend := withUser(base, "friend")
	var ownerMe, friendMe struct{ Admin bool }
	owner.do("GET", "/api/me", nil, &ownerMe)
	friend.do("GET", "/api/me", nil, &friendMe)
	if ownerMe.Admin || !friendMe.Admin {
		t.Fatalf("admin flags owner=%+v friend=%+v", ownerMe, friendMe)
	}
	var sources []sourceInfo
	friend.do("GET", "/api/sources?type=novel", nil, &sources)
	rule := sources[0].Rule
	rule["name"] = "friend-added-source"
	if code := owner.do("POST", "/api/sources", rule, nil); code != http.StatusForbidden {
		t.Fatalf("earliest user create source: %d, want 403", code)
	}
	if code := friend.do("POST", "/api/sources", rule, nil); code != http.StatusOK {
		t.Fatalf("configured admin create source: %d, want 200", code)
	}
}

func TestReadingFlow(t *testing.T) {
	c := newClient(t, config.Config{DevLogin: true})

	if code := c.do("GET", "/api/favorites", nil, nil); code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated request: got %d", code)
	}
	var login struct{ Token string }
	if code := c.do("POST", "/api/auth/login", map[string]string{"code": "x"}, &login); code != 200 || login.Token == "" {
		t.Fatalf("login: %d", code)
	}
	c.token = login.Token

	var found struct {
		Books []struct {
			ID    int64
			Title string
			Cover string
		}
	}
	c.do("GET", "/api/search?type=novel&q=%E6%98%9F%E6%B2%B3", nil, &found)
	if len(found.Books) != 1 || !strings.HasPrefix(found.Books[0].Cover, "/api/img?") {
		t.Fatalf("search: %+v", found)
	}
	id := found.Books[0].ID
	bookPath := "/api/books/" + itoa(id)

	var chapters []struct {
		ID     int64
		Title  string
		Cached bool
	}
	if code := c.do("GET", bookPath+"/chapters", nil, &chapters); code != 200 || len(chapters) != 30 {
		t.Fatalf("chapters: %d, %d items", code, len(chapters))
	}

	var ch struct {
		Text           string
		PrevID, NextID int64
	}
	c.do("GET", "/api/chapters/"+itoa(chapters[1].ID), nil, &ch)
	if !strings.Contains(ch.Text, "第2章第2页的最后一段") || ch.PrevID != chapters[0].ID || ch.NextID != chapters[2].ID {
		t.Fatalf("chapter: %+v", ch)
	}
	c.do("GET", bookPath+"/chapters", nil, &chapters)
	if !chapters[1].Cached {
		t.Fatal("chapter should be cached after it was read")
	}

	if code := c.do("PUT", "/api/favorites/"+itoa(id), nil, nil); code != http.StatusNoContent {
		t.Fatalf("add favorite: %d", code)
	}
	if code := c.do("PUT", "/api/history", map[string]any{"chapterId": chapters[1].ID, "position": 42}, nil); code != http.StatusNoContent {
		t.Fatalf("save progress: %d", code)
	}
	var shelf []struct {
		ID       int64
		Favorite bool
		Progress *struct {
			ChapterIdx int
			Position   int
		}
	}
	c.do("GET", "/api/favorites?type=novel", nil, &shelf)
	if len(shelf) != 1 || !shelf[0].Favorite || shelf[0].Progress == nil || shelf[0].Progress.ChapterIdx != 1 || shelf[0].Progress.Position != 42 {
		t.Fatalf("favorites: %+v", shelf)
	}
	c.do("GET", "/api/favorites?type=comic", nil, &shelf)
	if len(shelf) != 0 {
		t.Fatalf("novel leaked into the comic shelf: %+v", shelf)
	}
	c.do("GET", "/api/history", nil, &shelf)
	if len(shelf) != 1 {
		t.Fatalf("history: %+v", shelf)
	}
	c.do("DELETE", "/api/history/"+itoa(id), nil, nil)
	c.do("GET", "/api/history", nil, &shelf)
	if len(shelf) != 0 {
		t.Fatalf("history not cleared: %+v", shelf)
	}
}

func TestComicImagesGoThroughSignedProxy(t *testing.T) {
	c := newClient(t, config.Config{DevLogin: true})
	var login struct{ Token string }
	c.do("POST", "/api/auth/login", map[string]string{"code": "x"}, &login)
	c.token = login.Token

	var found struct{ Books []struct{ ID int64 } }
	c.do("GET", "/api/search?type=comic&q=%E8%89%B2%E5%9D%97", nil, &found)
	if len(found.Books) != 1 {
		t.Fatalf("search: %+v", found)
	}
	var chapters []struct{ ID int64 }
	c.do("GET", "/api/books/"+itoa(found.Books[0].ID)+"/chapters", nil, &chapters)
	var ch struct{ Images []string }
	c.do("GET", "/api/chapters/"+itoa(chapters[0].ID), nil, &ch)
	if len(ch.Images) != 6 {
		t.Fatalf("images: %+v", ch)
	}

	// No token: the signature alone authorizes the image.
	resp, err := http.Get(c.base + ch.Images[0])
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 || resp.Header.Get("Content-Type") != "image/png" {
		t.Fatalf("image proxy: %d %s", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	tampered := strings.Replace(ch.Images[0], "1.png", "2.png", 1)
	if resp, _ := http.Get(c.base + tampered); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("tampered image URL: got %d", resp.StatusCode)
	}
}

func TestConcurrentComicRequestsGetCleanImageLinks(t *testing.T) {
	c := newClient(t, config.Config{DevLogin: true})
	var login struct{ Token string }
	c.do("POST", "/api/auth/login", map[string]string{"code": "x"}, &login)
	c.token = login.Token

	var found struct{ Books []struct{ ID int64 } }
	c.do("GET", "/api/search?type=comic&q=%E8%89%B2%E5%9D%97", nil, &found)
	var chapters []struct{ ID int64 }
	c.do("GET", "/api/books/"+itoa(found.Books[0].ID)+"/chapters", nil, &chapters)

	// All of these share one fetch of the uncached chapter.
	results := make([][]string, 16)
	var wg sync.WaitGroup
	for i := range results {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var ch struct{ Images []string }
			c.do("GET", "/api/chapters/"+itoa(chapters[0].ID), nil, &ch)
			results[i] = ch.Images
		}()
	}
	wg.Wait()
	for _, images := range results {
		if len(images) != 6 {
			t.Fatalf("images: %v", images)
		}
		for _, img := range images {
			u, err := url.Parse(img)
			if err != nil || u.Path != "/api/img" || !strings.HasPrefix(u.Query().Get("u"), "http") {
				t.Fatalf("image link was rewritten more than once: %s", img)
			}
		}
	}
}

func TestEditingContentRuleDropsCachedChapters(t *testing.T) {
	c := newClient(t, config.Config{DevLogin: true})
	var login struct{ Token string }
	c.do("POST", "/api/auth/login", map[string]string{"code": "x"}, &login)
	c.token = login.Token

	var found struct{ Books []struct{ ID int64 } }
	c.do("GET", "/api/search?type=novel&q=%E6%98%9F%E6%B2%B3", nil, &found)
	var chapters []struct{ ID int64 }
	c.do("GET", "/api/books/"+itoa(found.Books[0].ID)+"/chapters", nil, &chapters)
	path := "/api/chapters/" + itoa(chapters[0].ID)
	var ch struct{ Text string }
	c.do("GET", path, nil, &ch)
	if !strings.Contains(ch.Text, "最后一段") {
		t.Fatalf("unexpected chapter text: %q", ch.Text)
	}

	var list []struct {
		ID   int64
		Type string
		Rule map[string]any
	}
	c.do("GET", "/api/sources?type=novel", nil, &list)
	rule := list[0].Rule

	// A rename keeps the cache; the text is unchanged.
	rule["name"] = "renamed"
	if code := c.do("PUT", "/api/sources/"+itoa(list[0].ID), rule, nil); code != http.StatusNoContent {
		t.Fatalf("rename: %d", code)
	}
	var cached []struct{ Cached bool }
	c.do("GET", "/api/books/"+itoa(found.Books[0].ID)+"/chapters", nil, &cached)
	if !cached[0].Cached {
		t.Fatal("renaming the source dropped the chapter cache")
	}

	// A new purify rule must apply to chapters that were already cached.
	rule["content"].(map[string]any)["purify"] = []string{".*最后一段。"}
	if code := c.do("PUT", "/api/sources/"+itoa(list[0].ID), rule, nil); code != http.StatusNoContent {
		t.Fatalf("update: %d", code)
	}
	ch.Text = ""
	c.do("GET", path, nil, &ch)
	if ch.Text == "" || strings.Contains(ch.Text, "最后一段") {
		t.Fatalf("chapter still served from the old rule's cache: %q", ch.Text)
	}
}

func TestSourceManagement(t *testing.T) {
	c := newClient(t, config.Config{DevLogin: true})
	var login struct{ Token string }
	c.do("POST", "/api/auth/login", map[string]string{"code": "x"}, &login)
	c.token = login.Token

	var list []struct {
		ID      int64
		Name    string
		Enabled bool
		Rule    json.RawMessage
	}
	c.do("GET", "/api/sources", nil, &list)
	if len(list) != 2 {
		t.Fatalf("sources: %+v", list)
	}

	var report map[string]any
	req, _ := http.NewRequest("POST", c.base+"/api/sources/test?q=%E5%B1%B1%E6%B5%B7", bytes.NewReader(list[0].Rule))
	req.Header.Set("Authorization", "Bearer "+c.token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	json.NewDecoder(resp.Body).Decode(&report)
	resp.Body.Close()
	if report["error"] != nil || report["chapterCount"] != float64(12) {
		t.Fatalf("test report: %+v", report)
	}

	if code := c.do("POST", "/api/sources", map[string]string{"name": "bad"}, nil); code != http.StatusBadRequest {
		t.Fatalf("invalid rule: got %d", code)
	}
	c.do("PUT", "/api/sources/"+itoa(list[0].ID)+"/enabled", map[string]bool{"enabled": false}, nil)
	var found struct{ Books []any }
	c.do("GET", "/api/search?type=novel&q=%E6%98%9F%E6%B2%B3", nil, &found)
	if len(found.Books) != 0 {
		t.Fatal("disabled source was still searched")
	}
	if code := c.do("DELETE", "/api/sources/"+itoa(list[0].ID), nil, nil); code != http.StatusNoContent {
		t.Fatalf("delete: %d", code)
	}
	c.do("GET", "/api/sources", nil, &list)
	if len(list) != 1 {
		t.Fatalf("sources after delete: %+v", list)
	}
}

func TestOnlyFirstWeChatUserMayRegisterWithoutWhitelist(t *testing.T) {
	// Without DEV_LOGIN and WeChat credentials, login must fail instead of letting anyone in.
	c := newClient(t, config.Config{})
	if code := c.do("POST", "/api/auth/login", map[string]string{"code": "x"}, nil); code == 200 {
		t.Fatal("login succeeded without WeChat credentials")
	}
}

func TestFailedLoginIsLoggedWithTheCallerButBounded(t *testing.T) {
	var logged bytes.Buffer
	log.SetOutput(&logged)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })

	c := newClient(t, config.Config{WXAppID: "wx-real", WXSecret: "s3cret"})
	req, _ := http.NewRequest("POST", c.base+"/api/auth/login", strings.NewReader(`{"code":"some-code"}`))
	req.Header.Set("Content-Type", "application/json")
	// An old build sends no appId; WeChat's Referer still names the AppID it runs under.
	req.Header.Set("Referer", "https://servicewechat.com/wx-other/devtools/page-frame.html")
	req.Header.Set("User-Agent", strings.Repeat("A", 100_000))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest || !strings.Contains(string(body), "wx-other") {
		t.Fatalf("login = %d %s, want a 400 naming the AppID from the Referer", resp.StatusCode, body)
	}

	line := logged.String()
	if !strings.Contains(line, "servicewechat.com/wx-other/") {
		t.Fatalf("log does not name the caller: %q", line)
	}
	if len(line) > 2000 || strings.Count(line, "\n") != 1 {
		t.Fatalf("log entry is %d bytes on %d lines", len(line), strings.Count(line, "\n"))
	}
	if strings.Contains(line, "some-code") || strings.Contains(line, "s3cret") {
		t.Fatalf("log leaks a credential: %q", line)
	}
}

func itoa(n int64) string {
	raw, _ := json.Marshal(n)
	return string(raw)
}

func TestSearchBySourceAndExactTitle(t *testing.T) {
	c := login(t)
	c.addCopy("copy", "")
	var copyID int64
	for _, s := range c.sources("?type=novel") {
		if s.Name == "copy" {
			copyID = s.ID
		}
	}
	titles := func(query string) []string {
		t.Helper()
		var r searchResult
		if code := c.do("GET", "/api/search?type=novel&q="+url.QueryEscape("灯火")+query, nil, &r); code != 200 {
			t.Fatalf("search %q: %d", query, code)
		}
		out := []string{}
		for _, b := range r.Books {
			out = append(out, b.SourceName+"/"+b.Title)
		}
		return out
	}

	if got := titles(""); len(got) != 4 {
		t.Fatalf("both sources: %v", got)
	}
	got := titles("&source=" + itoa(copyID))
	if len(got) != 2 || got[0] != "copy/长夜灯火" || got[1] != "copy/长夜灯火" {
		t.Fatalf("one source: %v", got)
	}
	if got := titles("&match=exact"); len(got) != 0 {
		t.Fatalf("exact search for part of a title: %v", got)
	}
	if code := c.do("GET", "/api/search?type=novel&q=x&source=abc", nil, nil); code != http.StatusBadRequest {
		t.Fatalf("bad source: %d", code)
	}

	// A keyword may match the author; an exact search keeps only the books of that very title.
	var r searchResult
	c.do("GET", "/api/search?type=novel&source="+itoa(copyID)+"&q="+url.QueryEscape("演示作者乙"), nil, &r)
	if len(r.Books) != 3 {
		t.Fatalf("author search: %+v", r.Books)
	}
	c.do("GET", "/api/search?type=novel&match=exact&q="+url.QueryEscape("长夜灯火"), nil, &r)
	if len(r.Books) != 4 {
		t.Fatalf("exact search: %+v", r.Books)
	}
	for _, b := range r.Books {
		if b.Title != "长夜灯火" {
			t.Fatalf("exact search returned %q", b.Title)
		}
	}
}

func TestRecommendListsTopTenPerSource(t *testing.T) {
	c := login(t)
	type group struct {
		SourceID   int64
		SourceName string
		Books      []struct {
			ID           int64
			Title, Cover string
		}
	}
	var novels, comics []group
	c.do("GET", "/api/recommend?type=novel", nil, &novels)
	if len(novels) != 1 || novels[0].SourceName != "演示小说源" || len(novels[0].Books) != 10 {
		t.Fatalf("novel recommendations: %+v", novels)
	}
	first := novels[0].Books[0]
	if first.Title != "演示小说：星河旅人" || !strings.HasPrefix(first.Cover, "/api/img?") {
		t.Fatalf("first recommendation: %+v", first)
	}
	if code := c.do("GET", "/api/books/"+itoa(first.ID), nil, nil); code != 200 {
		t.Fatalf("open recommended book: %d", code)
	}
	c.do("GET", "/api/recommend?type=comic", nil, &comics)
	if len(comics) != 1 || len(comics[0].Books) != 4 {
		t.Fatalf("comic recommendations: %+v", comics)
	}

	// A source without a recommend rule and one that cannot be reached are left out quietly;
	// the cover links of the cached list stay valid on the second request.
	rule := c.sources("?type=novel")[0].Rule
	delete(rule, "recommend")
	rule["name"] = "plain"
	c.do("POST", "/api/sources", rule, nil)
	c.addCopy("dead", "http://127.0.0.1:1")
	var again []group
	if code := c.do("GET", "/api/recommend?type=novel", nil, &again); code != 200 || len(again) != 1 {
		t.Fatalf("recommendations with a plain and a dead source: %d %+v", code, again)
	}
	if again[0].Books[0].Cover != first.Cover {
		t.Fatalf("cover changed between requests: %q, %q", first.Cover, again[0].Books[0].Cover)
	}
	for _, s := range c.sources("?type=novel") {
		if s.Health != nil {
			t.Errorf("a failed recommendation counted against source %q", s.Name)
		}
	}

	var one []group
	c.do("GET", "/api/recommend?type=novel&source="+itoa(novels[0].SourceID), nil, &one)
	if len(one) != 1 || one[0].SourceID != novels[0].SourceID {
		t.Fatalf("recommendations of one source: %+v", one)
	}

	// The list read earlier is kept while the site is down.
	c.site.Close()
	c.do("GET", "/api/recommend?type=novel", nil, &again)
	if len(again) != 1 || len(again[0].Books) != 10 {
		t.Fatalf("recommendations during an outage: %+v", again)
	}

	bad := c.sources("?type=novel")[0].Rule
	bad["name"] = "bad"
	bad["recommend"] = map[string]any{"url": "/rank", "list": "css:.result li"}
	if code := c.do("POST", "/api/sources", bad, nil); code != http.StatusBadRequest {
		t.Fatalf("incomplete recommend rule: %d", code)
	}
}

// files counts the files below a directory of the data dir.
func (c *client) files(dir string) (n int) {
	filepath.WalkDir(filepath.Join(c.dir, dir), func(_ string, e os.DirEntry, err error) error {
		if err == nil && !e.IsDir() {
			n++
		}
		return nil
	})
	return n
}

func TestFavoritedComicIsSavedOnTheServer(t *testing.T) {
	c := newClient(t, config.Config{DevLogin: true, RefreshInterval: 300 * time.Millisecond, KeptMaxMB: 100})
	var login struct{ Token string }
	c.do("POST", "/api/auth/login", map[string]string{"code": "x"}, &login)
	c.token = login.Token
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	go c.lib.Run(ctx)

	var found struct{ Books []struct{ ID int64 } }
	c.do("GET", "/api/search?type=comic&q=%E8%89%B2%E5%9D%97", nil, &found)
	id := itoa(found.Books[0].ID)
	var chapters []struct{ ID int64 }
	c.do("GET", "/api/books/"+id+"/chapters", nil, &chapters)
	var ch struct{ Images, Origins []string }
	c.do("GET", "/api/chapters/"+itoa(chapters[0].ID), nil, &ch)
	if len(ch.Origins) != len(ch.Images) || !strings.HasPrefix(ch.Origins[0], c.site.URL) {
		t.Fatalf("origins: %+v", ch)
	}
	// A page that was read before the book was favorited sits in the cache.
	if resp, err := http.Get(c.base + ch.Images[0]); err != nil || resp.StatusCode != 200 {
		t.Fatalf("image: %v", err)
	}
	if c.files("img") != 1 || c.files("kept") != 0 {
		t.Fatalf("before the favorite: %d cached, %d kept", c.files("img"), c.files("kept"))
	}

	if code := c.do("PUT", "/api/favorites/"+id, nil, nil); code != http.StatusNoContent {
		t.Fatalf("add favorite: %d", code)
	}
	var tasks []struct{ Status string }
	for deadline := time.Now().Add(20 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		c.do("GET", "/api/tasks", nil, &tasks)
		if len(tasks) == 1 && tasks[0].Status == "done" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the comic was not saved: %+v", tasks)
		}
	}
	pages := len(chapters) * len(ch.Images)
	if c.files("kept") != pages || c.files("img") != 0 {
		t.Fatalf("after the favorite: %d cached, %d kept, want %d kept", c.files("img"), c.files("kept"), pages)
	}
	// The cache eviction leaves the saved pages alone.
	if n := cache.New(c.dir).EvictImages(0); n != 0 || c.files("kept") != pages {
		t.Fatalf("eviction removed %d files, %d kept", n, c.files("kept"))
	}
	if resp, err := http.Get(c.base + ch.Images[0]); err != nil || resp.StatusCode != 200 {
		t.Fatalf("saved image: %v", err)
	}
	// Pages that are not saved, for whatever reason, are fetched by the periodic refresh.
	os.RemoveAll(filepath.Join(c.dir, "kept"))
	for deadline := time.Now().Add(20 * time.Second); c.files("kept") != pages; time.Sleep(20 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("after the refresh: %d kept", c.files("kept"))
		}
	}

	c.do("DELETE", "/api/favorites/"+id, nil, nil)
	if c.files("kept") != 0 || c.files("img") != pages {
		t.Fatalf("after leaving the shelf: %d cached, %d kept", c.files("img"), c.files("kept"))
	}

	// Back on the shelf the cached pages are kept again, until the source is deleted.
	c.do("PUT", "/api/favorites/"+id, nil, nil)
	for deadline := time.Now().Add(20 * time.Second); c.files("kept") != pages; time.Sleep(20 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("back on the shelf: %d kept", c.files("kept"))
		}
	}
	for _, src := range c.sources("?type=comic") {
		c.do("DELETE", "/api/sources/"+itoa(src.ID), nil, nil)
	}
	if c.files("kept") != 0 {
		t.Fatalf("after deleting the source: %d kept", c.files("kept"))
	}
}

func TestAPageThatIsGoneDoesNotStopTheRestOfAComicFromBeingSaved(t *testing.T) {
	c := newClient(t, config.Config{DevLogin: true, RefreshInterval: 300 * time.Millisecond, KeptMaxMB: 100})
	var login struct{ Token string }
	c.do("POST", "/api/auth/login", map[string]string{"code": "x"}, &login)
	c.token = login.Token
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	go c.lib.Run(ctx)

	var found struct{ Books []struct{ ID int64 } }
	c.do("GET", "/api/search?type=comic&q=%E8%89%B2%E5%9D%97", nil, &found)
	id := itoa(found.Books[0].ID)
	var chapters []struct{ ID int64 }
	c.do("GET", "/api/books/"+id+"/chapters", nil, &chapters)
	var ch struct{ Origins []string }
	c.do("GET", "/api/chapters/"+itoa(chapters[0].ID), nil, &ch)
	// The first page of the first chapter: every other page comes after it.
	gone, _ := url.Parse(ch.Origins[0])
	c.gone.Store(&gone.Path)
	want := len(chapters)*len(ch.Origins) - 1

	c.do("PUT", "/api/favorites/"+id, nil, nil)
	saved := func(by string) {
		t.Helper()
		for deadline := time.Now().Add(20 * time.Second); c.files("kept") != want; time.Sleep(20 * time.Millisecond) {
			if time.Now().After(deadline) {
				t.Fatalf("%s: %d kept, want %d", by, c.files("kept"), want)
			}
		}
	}
	saved("the job of the favorite")
	os.RemoveAll(filepath.Join(c.dir, "kept"))
	saved("the periodic refresh")
}

func TestRefreshEndpoint(t *testing.T) {
	c := newClient(t, config.Config{DevLogin: true})
	var session struct{ Token, RefreshToken string }
	c.do("POST", "/api/auth/login", map[string]string{"code": "x"}, &session)
	if session.Token == "" || session.RefreshToken == "" {
		t.Fatalf("login: %+v", session)
	}
	if code := c.do("POST", "/api/auth/refresh", map[string]string{"refreshToken": session.Token}, nil); code != http.StatusUnauthorized {
		t.Fatalf("refresh with an access token: got %d", code)
	}
	var next struct{ Token, RefreshToken string }
	if code := c.do("POST", "/api/auth/refresh", map[string]string{"refreshToken": session.RefreshToken}, &next); code != http.StatusOK {
		t.Fatalf("refresh: got %d", code)
	}
	c.token = next.Token
	if code := c.do("GET", "/api/favorites", nil, nil); code != http.StatusOK {
		t.Fatalf("request with the renewed token: got %d", code)
	}
	c.token = next.RefreshToken
	if code := c.do("GET", "/api/favorites", nil, nil); code != http.StatusUnauthorized {
		t.Fatalf("request with a refresh token: got %d", code)
	}
}

func TestImportReplacesTheSourceOfTheSameName(t *testing.T) {
	c := newClient(t, config.Config{DevLogin: true})
	var login struct{ Token string }
	c.do("POST", "/api/auth/login", map[string]string{"code": "x"}, &login)
	c.token = login.Token

	type row struct {
		ID   int64
		Name string
		Rule json.RawMessage
	}
	var list []row
	c.do("GET", "/api/sources", nil, &list)
	var rule map[string]any
	if err := json.Unmarshal(list[0].Rule, &rule); err != nil {
		t.Fatal(err)
	}

	var out struct {
		IDs     []int64
		Updated int
	}
	rule["rateLimitMs"] = 7
	renamed := maps.Clone(rule)
	renamed["name"] = "another name"
	c.do("POST", "/api/sources", []any{rule, renamed}, &out)
	if out.Updated != 1 || len(out.IDs) != 2 || out.IDs[0] != list[0].ID || out.IDs[1] == list[0].ID {
		t.Fatalf("import: %+v, want the first rule to replace source %d", out, list[0].ID)
	}
	fresh := maps.Clone(rule)
	fresh["name"] = "twice"
	if code := c.do("POST", "/api/sources", []any{fresh, fresh}, nil); code != http.StatusBadRequest {
		t.Errorf("a list naming one source twice: status %d, want 400", code)
	}
	var after []row
	c.do("GET", "/api/sources", nil, &after)
	if len(after) != len(list)+1 {
		t.Fatalf("%d sources after the import, want %d", len(after), len(list)+1)
	}
	var stored map[string]any
	_ = json.Unmarshal(after[0].Rule, &stored)
	if after[0].ID != list[0].ID || stored["rateLimitMs"] != float64(7) {
		t.Errorf("source %d was not updated in place: %s", list[0].ID, after[0].Rule)
	}
}

func TestRankAndFeed(t *testing.T) {
	c := login(t)
	type book struct {
		ID, SourceID int64
		Title, Cover string
		Sources      int
		Reason       string
	}
	var rank struct {
		SourceName string
		Total      int
		Books      []book
	}
	src := c.sources("?type=novel")[0]
	c.do("GET", "/api/rank?type=novel&source="+itoa(src.ID)+"&limit=4", nil, &rank)
	if rank.SourceName != src.Name || len(rank.Books) != 4 || rank.Total <= 10 {
		t.Fatalf("first page of the ranking: %+v", rank)
	}
	total, first := rank.Total, rank.Books[0]
	if first.Title != "演示小说：星河旅人" || !strings.HasPrefix(first.Cover, "/api/img?") {
		t.Fatalf("first of the ranking: %+v", first)
	}
	c.do("GET", "/api/rank?source="+itoa(src.ID)+"&offset="+itoa(int64(total-2))+"&limit=4", nil, &rank)
	if len(rank.Books) != 2 {
		t.Fatalf("last page of the ranking: %d books", len(rank.Books))
	}
	if code := c.do("GET", "/api/rank?type=novel", nil, nil); code != http.StatusBadRequest {
		t.Fatalf("ranking without a source: %d", code)
	}

	// Two sources with the same books: each book is recommended once.
	c.addCopy("copy", "")
	var feed struct {
		Total int
		Books []book
	}
	// (The demo site has two books of one title by different authors, which stay two.)
	seen := map[int64]bool{}
	for offset := 0; offset == 0 || offset < feed.Total; offset += 5 {
		c.do("GET", "/api/feed?type=novel&limit=5&offset="+itoa(int64(offset)), nil, &feed)
		for _, b := range feed.Books {
			if seen[b.ID] || b.Sources != 2 || !strings.HasPrefix(b.Cover, "/api/img?") {
				t.Fatalf("feed book %+v, seen before: %v", b, seen[b.ID])
			}
			seen[b.ID] = true
		}
	}
	if feed.Total != total || len(seen) != total {
		t.Fatalf("feed of %d books (%d read) from two lists of %d", feed.Total, len(seen), total)
	}

	// A book the reader has opened or put on the shelf is not recommended again.
	c.do("PUT", "/api/favorites/"+itoa(first.ID), nil, nil)
	c.do("GET", "/api/feed?type=novel&limit=50", nil, &feed)
	if feed.Total != total-1 {
		t.Fatalf("feed of %d books with one of %d on the shelf", feed.Total, total)
	}
	for _, b := range feed.Books {
		if b.Title == first.Title {
			t.Fatalf("%q is on the shelf and still recommended", b.Title)
		}
	}
}

// With many sources there is always one that does not answer: the feed shows the lists that
// were read and does not wait for it.
func TestFeedDoesNotWaitForASlowSource(t *testing.T) {
	c := login(t)
	var feed struct{ Total int }
	c.do("GET", "/api/feed?type=novel", nil, &feed)
	total := feed.Total
	if total == 0 {
		t.Fatal("no feed from the demo source")
	}

	gone := make(chan struct{})
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-gone:
		case <-r.Context().Done():
		}
	}))
	defer slow.Close()
	defer close(gone)
	c.addCopy("slow", slow.URL)

	// The new source is given a moment once; after that nobody waits for it.
	c.do("GET", "/api/feed?type=novel", nil, &feed)
	start := time.Now()
	c.do("GET", "/api/feed?type=novel", nil, &feed)
	if took := time.Since(start); took > time.Second || feed.Total != total {
		t.Fatalf("feed of %d books (want %d) after %v", feed.Total, total, took)
	}
}

// A search answers with what the fast sources found; the slow one is named as still searching.
func TestSearchDoesNotWaitForASlowSource(t *testing.T) {
	c := login(t)
	gone := make(chan struct{})
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-gone:
		case <-r.Context().Done():
		}
	}))
	defer slow.Close()
	defer close(gone)
	c.addCopy("slow", slow.URL)

	var r searchResult
	start := time.Now()
	c.do("GET", qStar, nil, &r)
	if took := time.Since(start); took > 4*time.Second || len(r.Books) == 0 ||
		len(r.Errors) != 1 || r.Errors[0].Source != "slow" || r.Errors[0].Kind != "pending" {
		t.Fatalf("after %v: %d books, errors %+v", took, len(r.Books), r.Errors)
	}
	// One source alone is waited for; here the reader gives up first.
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", c.base+qStar+"&source="+itoa(c.sources("?type=novel")[1].ID), nil)
	req.Header.Set("Authorization", "Bearer "+c.token)
	if resp, err := http.DefaultClient.Do(req); err == nil {
		resp.Body.Close()
		t.Fatalf("a search in the slow source alone answered with %d", resp.StatusCode)
	}
}
