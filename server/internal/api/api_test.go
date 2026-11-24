package api_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/GeiQianJiuGan/sleepy-bookmark/server/internal/api"
	"github.com/GeiQianJiuGan/sleepy-bookmark/server/internal/auth"
	"github.com/GeiQianJiuGan/sleepy-bookmark/server/internal/cache"
	"github.com/GeiQianJiuGan/sleepy-bookmark/server/internal/config"
	"github.com/GeiQianJiuGan/sleepy-bookmark/server/internal/demo"
	"github.com/GeiQianJiuGan/sleepy-bookmark/server/internal/library"
	"github.com/GeiQianJiuGan/sleepy-bookmark/server/internal/store"
)

type client struct {
	t     *testing.T
	base  string
	token string
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
	return &client{t: t, base: srv.URL}
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
