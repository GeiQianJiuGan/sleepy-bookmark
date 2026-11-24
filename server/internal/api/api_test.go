package api_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
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
)

type client struct {
	t     *testing.T
	base  string
	token string
	// site is the demo site that the seeded sources point at.
	site *httptest.Server
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
	site := httptest.NewServer(demo.Handler())
	t.Cleanup(site.Close)

	cfg.DataDir = t.TempDir()
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
	return &client{t: t, base: srv.URL, site: site}
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
	bad["recommend"] = map[string]any{"url": "/rank"}
	if code := c.do("POST", "/api/sources", bad, nil); code != http.StatusBadRequest {
		t.Fatalf("incomplete recommend rule: %d", code)
	}
}
