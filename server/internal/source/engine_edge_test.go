package source_test

import (
	"context"
	"fmt"
	"io"
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

func TestContentStopsAtARedirectToAPageItAlreadyRead(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/c/1", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<html><body><img src="/i/1.jpg"><img src="/i/2.jpg"><a id="next" href="/c/1_2">next</a></body></html>`))
	})
	mux.HandleFunc("/c/1_2", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<html><body><img src="/i/3.jpg"><a id="next" href="/c/1_3">next</a></body></html>`))
	})
	// The last page links to a page that does not exist, and the site sends that to the first one.
	mux.HandleFunc("/c/1_3", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/c/1", http.StatusFound)
	})
	// The same for a link that the site sends to the next chapter.
	mux.HandleFunc("/c/2", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<html><body><img src="/i/4.jpg"><a id="next" href="/c/2_2">next</a></body></html>`))
	})
	mux.HandleFunc("/c/2_2", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/c/3", http.StatusFound)
	})
	mux.HandleFunc("/c/3", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<html><body><img src="/i/5.jpg"></body></html>`))
	})
	e, src := site(t, mux)
	src.Type = source.TypeComic
	src.Content = source.ContentRule{Images: "css:img@src", NextPage: "css:#next@href"}

	c, err := e.Content(context.Background(), src, src.BaseURL+"/c/1", src.BaseURL+"/c/2")
	if err != nil || len(c.Images) != 3 {
		t.Fatalf("images: %v, %v", c.Images, err)
	}
	c, err = e.Content(context.Background(), src, src.BaseURL+"/c/2", src.BaseURL+"/c/3")
	if err != nil || len(c.Images) != 1 {
		t.Fatalf("images before the next chapter: %v, %v", c.Images, err)
	}
}

func TestTocFallbackReadsAShortCatalog(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/long", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<div class="latest"><a href="/c/2">two</a></div><div id="list"><a href="/c/1">one</a><a href="/c/2">two</a></div>`))
	})
	mux.HandleFunc("/short", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<div class="latest"><a href="/c/2">two</a><a href="/c/1">one</a></div>`))
	})
	e, src := site(t, mux)
	src.Toc.Fallback = &source.TocRule{List: "css:.latest a", Name: "@text", URL: "@href", Reverse: true}
	if err := src.Validate(); err != nil {
		t.Fatal(err)
	}

	for _, page := range []string{"/long", "/short"} {
		toc, err := e.Toc(context.Background(), src, src.BaseURL+page)
		if err != nil || len(toc) != 2 || toc[0].Title != "one" || toc[1].Title != "two" {
			t.Fatalf("%s: %+v, %v", page, toc, err)
		}
	}
}

// A site that is forced to GBK reads the search keyword as GBK, in the query and in a form.
func TestSearchKeywordUsesTheSiteCharset(t *testing.T) {
	const gbk = "%B7%B2%C8%CB" // 凡人
	var query, form string
	mux := http.NewServeMux()
	mux.HandleFunc("/search", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		query, form = r.URL.RawQuery, string(body)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, `<li><a href="/book/1">x</a></li>`)
	})
	e, src := site(t, mux)
	src.Search.URL, src.Search.Method, src.Search.Body = "/search?q={{key}}", "POST", "s={{key}}"

	for charset, want := range map[string]string{"gbk": gbk, "": "%E5%87%A1%E4%BA%BA", "no-such-charset": "%E5%87%A1%E4%BA%BA"} {
		src.Charset = charset
		if _, err := e.Search(context.Background(), src, "凡人", 1); err != nil {
			t.Fatal(err)
		}
		if query != "q="+want || form != "s="+want {
			t.Errorf("charset %q: query %q, form %q, want %s", charset, query, form, want)
		}
	}
}
