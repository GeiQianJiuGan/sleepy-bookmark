package source_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
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

// A catalog or chapter with more pages than the engine follows is an error, not a short result
// that would be cached as if it were complete.
func TestTooManyPagesIsAnError(t *testing.T) {
	mux := http.NewServeMux()
	page := func(w http.ResponseWriter, r *http.Request) {
		n, _ := strconv.Atoi(r.URL.Query().Get("p"))
		fmt.Fprintf(w, `<div id="list"><a href="/c/%d">c%d</a></div><div id="content">text %d</div><a id="next" href="?p=%d">next</a>`, n, n, n, n+1)
	}
	mux.HandleFunc("/toc", page)
	mux.HandleFunc("/chapter", page)
	e, src := site(t, mux)
	src.Toc.NextPage = "css:#next@href"
	src.Content.NextPage = "css:#next@href"

	if toc, err := e.Toc(context.Background(), src, src.BaseURL+"/toc"); err == nil {
		t.Fatalf("toc: got %d chapters and no error", len(toc))
	}
	if c, err := e.Content(context.Background(), src, src.BaseURL+"/chapter", ""); err == nil {
		t.Fatalf("content: got %d bytes and no error", len(c.Text))
	}
}

// The last catalog page links back to the first one by its redirected address.
func TestPaginationStopsAtARedirectedPageItAlreadyRead(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/toc", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/toc/", http.StatusFound)
	})
	mux.HandleFunc("/toc/", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<div id="list"><a href="/c/1">one</a></div><a id="next" href="/toc/2">next</a>`))
	})
	mux.HandleFunc("/toc/2", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<div id="list"><a href="/c/2">two</a></div><a id="next" href="/toc/">next</a>`))
	})
	e, src := site(t, mux)
	src.Toc.NextPage = "css:#next@href"

	toc, err := e.Toc(context.Background(), src, src.BaseURL+"/toc")
	if err != nil || len(toc) != 2 {
		t.Fatalf("toc: %+v, %v", toc, err)
	}
}

func TestComicPagesOverlapIsReadOnce(t *testing.T) {
	mux := http.NewServeMux()
	// 1.jpg is shown twice on purpose; 2.jpg and 3.jpg are repeated at the start of page two.
	mux.HandleFunc("/c/1", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<html><body><img src="/i/1.jpg"><img src="/i/1.jpg"><img src="/i/2.jpg"><img src="/i/3.jpg"><a id="next" href="/c/1_2">next</a></body></html>`))
	})
	mux.HandleFunc("/c/1_2", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<html><body><img src="/i/2.jpg"><img src="/i/3.jpg"><img src="/i/4.jpg"><img src="/i/1.jpg"></body></html>`))
	})
	e, src := site(t, mux)
	src.Type = source.TypeComic
	src.Content = source.ContentRule{Images: "css:img@src", NextPage: "css:#next@href"}

	c, err := e.Content(context.Background(), src, src.BaseURL+"/c/1", "")
	var want []string
	for _, n := range []int{1, 1, 2, 3, 4, 1} {
		want = append(want, fmt.Sprintf("%s/i/%d.jpg", src.BaseURL, n))
	}
	if err != nil || !slices.Equal(c.Images, want) {
		t.Fatalf("images: %v, %v", c.Images, err)
	}
}
