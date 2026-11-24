package source_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/GeiQianJiuGan/sleepy-bookmark/server/internal/source"
)

// site serves the given handlers and returns a novel source rule pointing at it.
func site(t *testing.T, mux *http.ServeMux) (*source.Engine, *source.Source) {
	t.Helper()
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	src := &source.Source{
		Name: "t", Type: source.TypeNovel, BaseURL: srv.URL, RateLimitMs: 1,
		Search:  source.SearchRule{URL: "/search", List: "css:li", Title: "css:a", URLRule: "css:a@href"},
		Toc:     source.TocRule{List: "css:#list a", Name: "@text", URL: "@href"},
		Content: source.ContentRule{Body: "css:#content"},
	}
	return &source.Engine{Fetcher: source.NewFetcher()}, src
}

func TestJSONNovelBodyWithoutPrefix(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/chapter/1", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"data":{"content":"<p>first</p><p>second</p>"}}`))
	})
	e, src := site(t, mux)
	src.Content.Body = "data.content"

	c, err := e.Content(context.Background(), src, src.BaseURL+"/chapter/1", "")
	if err != nil || c.Text != "first\nsecond" {
		t.Fatalf("content: %q, %v", c.Text, err)
	}
}

func TestRelativeLinksFollowRedirects(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/book/1", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/book/1/", http.StatusMovedPermanently)
	})
	mux.HandleFunc("/book/1/", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<html><body><div id="list"><a href="1.html">one</a></div></body></html>`))
	})
	e, src := site(t, mux)

	toc, err := e.Toc(context.Background(), src, src.BaseURL+"/book/1")
	if err != nil || len(toc) != 1 || toc[0].URL != src.BaseURL+"/book/1/1.html" {
		t.Fatalf("toc: %+v, %v", toc, err)
	}
	// The second read comes from the page cache and must resolve the same way.
	toc, err = e.Toc(context.Background(), src, src.BaseURL+"/book/1")
	if err != nil || toc[0].URL != src.BaseURL+"/book/1/1.html" {
		t.Fatalf("cached toc: %+v, %v", toc, err)
	}
}

func TestPageCacheIsPerHeadersAndCharset(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/chapter/1", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<div id="content">` + r.Header.Get("X-Edition") + `</div>`))
	})
	e, src := site(t, mux)
	read := func(edition string) string {
		t.Helper()
		src.Headers = map[string]string{"X-Edition": edition}
		c, err := e.Content(context.Background(), src, src.BaseURL+"/chapter/1", "")
		if err != nil {
			t.Fatal(err)
		}
		return c.Text
	}
	if a, b := read("first"), read("second"); a != "first" || b != "second" {
		t.Fatalf("got %q then %q", a, b)
	}
}
