package source_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/GeiQianJiuGan/sleepy-bookmark/server/internal/source"
)

// The sites below are made up. They are laid out the way sites of their kind commonly are, with
// what gets in the way of reading them: menus, footers, lists of other books, repeated links.

type title struct {
	id           int
	name, author string
}

var shelf = []title{
	{11, "雾港钟声", "林见"}, {12, "雾港旧事", "林见"}, {13, "盐与铁", "周北"}, {14, "白塔来客", "沈青"},
	{15, "长堤", "周北"}, {16, "风从南来", "何田"}, {17, "七号码头", "林见"}, {18, "纸鸢", "沈青"},
}

const frame = `<html><head><meta charset="utf-8">%s</head><body>
<div class="nav"><a href="/">首页</a><a href="/sort/1/">玄幻</a><a href="/sort/2/">都市</a><a href="/sort/3/">历史</a>
<a href="/sort/4/">科幻</a><a href="/sort/5/">悬疑</a><a href="/sort/6/">完本</a><a href="/top/">排行</a></div>
%s
<div class="footer"><a href="/about.html">关于本站</a><a href="/sitemap.html">网站地图</a></div></body></html>`

// novelSite is a novel site of the common kind: a search form on every page, tables of results,
// a book page that repeats the newest chapters above a paged catalog, chapters in two parts.
func novelSite() http.Handler {
	const form = `<form class="search" action="/search.php" method="post"><input type="hidden" name="type" value="all">
<input name="searchkey" type="text"><button>搜索</button></form>`
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		var hot, updates strings.Builder
		for _, b := range shelf[:6] {
			fmt.Fprintf(&hot, `<div class="item"><a href="/book/%d/"><img data-original="/cover/%d.jpg" src="/img/blank.gif" alt="%s"></a>
<h3><a href="/book/%d/">%s</a></h3><p>作者：%s</p></div>`, b.id, b.id, b.name, b.id, b.name, b.author)
		}
		for _, b := range shelf {
			fmt.Fprintf(&updates, `<li><span><a href="/book/%d/">%s</a></span><span><a href="/book/%d/40.html">第40章 归途</a></span><span>%s</span></li>`,
				b.id, b.name, b.id, b.author)
		}
		fmt.Fprintf(w, frame, "", form+`<div class="hot">`+hot.String()+`</div><ul class="updates">`+updates.String()+`</ul>`)
	})
	mux.HandleFunc("POST /search.php", func(w http.ResponseWriter, r *http.Request) {
		key := r.FormValue("searchkey")
		if r.FormValue("type") != "all" || key == "" {
			http.Error(w, "bad search", http.StatusBadRequest)
			return
		}
		var rows strings.Builder
		for _, b := range shelf {
			if strings.Contains(b.name, key) || strings.Contains(b.author, key) {
				fmt.Fprintf(&rows, `<tr><td><a href="/book/%d/">%s</a></td><td><a href="/book/%d/40.html">第40章 归途</a></td><td>%s</td></tr>`,
					b.id, b.name, b.id, b.author)
			}
		}
		// The sidebar lists books that were not searched for.
		side := `<div class="side"><a href="/book/13/">盐与铁</a><a href="/book/14/">白塔来客</a><a href="/book/15/">长堤</a></div>`
		fmt.Fprintf(w, frame, "", form+`<table class="grid">`+rows.String()+`</table>`+side)
	})
	page := func(w http.ResponseWriter, r *http.Request) {
		id, file := r.PathValue("id"), r.PathValue("file")
		var n, part int
		switch {
		case file == "index_2.html":
			part = 2
		case strings.Contains(file, "_"):
			fmt.Sscanf(file, "%d_%d.html", &n, &part)
		default:
			fmt.Sscanf(file, "%d.html", &n)
		}
		if n == 0 {
			// The catalog: 20 chapters a page, the three newest repeated above the first.
			var list strings.Builder
			if part == 0 {
				list.WriteString(`<dl><dt>最新章节</dt>`)
				for c := 40; c > 37; c-- {
					fmt.Fprintf(&list, `<dd><a href="/book/%s/%d.html">第%d章 归途</a></dd>`, id, c, c)
				}
				list.WriteString(`</dl>`)
			}
			list.WriteString(`<dl><dt>正文</dt>`)
			from := 1
			if part == 2 {
				from = 21
			}
			for c := from; c < from+20; c++ {
				fmt.Fprintf(&list, `<dd><a href="/book/%s/%d.html">第%d章 归途</a></dd>`, id, c, c)
			}
			list.WriteString(`</dl>`)
			if part == 0 {
				fmt.Fprintf(&list, `<div class="pages"><a href="/book/%s/index_2.html">下一页</a></div>`, id)
			}
			head := `<meta property="og:novel:book_name" content="雾港钟声"><meta property="og:novel:author" content="林见">
<meta property="og:image" content="/cover/11.jpg"><meta property="og:description" content="港口的钟每夜响十三下。">
<meta property="og:novel:latest_chapter_name" content="第40章 归途">`
			related := `<div class="related"><a href="/book/12/">雾港旧事</a><a href="/book/13/">盐与铁</a><a href="/book/14/">白塔来客</a>
<a href="/book/15/">长堤</a><a href="/book/16/">风从南来</a><a href="/book/17/">七号码头</a></div>`
			fmt.Fprintf(w, frame, head, `<h1>雾港钟声</h1><h1 class="logo">某站</h1>`+list.String()+related)
			return
		}
		next := fmt.Sprintf(`<a href="/book/%s/%d_2.html">下一页</a>`, id, n)
		if part == 2 {
			next = fmt.Sprintf(`<a href="/book/%s/%d.html">下一章</a>`, id, n+1)
		}
		if part == 0 {
			part = 1
		}
		fmt.Fprintf(w, frame, "", fmt.Sprintf(`<div class="path"><a href="/book/%s/">雾港钟声</a> &gt; 第%d章</div><h1>第%d章 归途</h1>
<div class="tip">喜欢本书请加入书架，方便下次阅读。把本站分享给朋友吧。字体大小可以在设置里调整，夜间模式同样在那里。</div>
<div id="chaptercontent"><p>第%d章第%d部分的第一段，船在雾里靠了岸。</p><p>第二段，<a href="/app.html">下载客户端</a>钟声从塔上传下来。</p>
<script>var x = "不该出现的脚本";</script><p>最后一段。</p></div>
<div class="bottom"><a href="/book/%s/">目录</a>%s</div>`, id, n, n, n, part, id, next))
	}
	mux.HandleFunc("GET /book/{id}/{$}", page)
	mux.HandleFunc("GET /book/{id}/{file}", page)
	return mux
}

func autoSource(t *testing.T, kind string, site http.Handler) (*source.Engine, *source.Source) {
	t.Helper()
	srv := httptest.NewServer(site)
	t.Cleanup(srv.Close)
	src := &source.Source{Name: "只有名称和地址", Type: kind, BaseURL: srv.URL + "/"}
	if err := src.Validate(); err != nil {
		t.Fatal(err)
	}
	if !src.IsAuto() {
		t.Fatalf("a source of a name and an address is not left to recognition: %+v", src)
	}
	src.RateLimitMs = 1
	return &source.Engine{Fetcher: source.NewFetcher()}, src
}

func TestANovelSiteIsReadFromItsAddressAlone(t *testing.T) {
	e, src := autoSource(t, source.TypeNovel, novelSite())
	ctx := context.Background()

	// The search form posts a hidden field along; the sidebar's books are not results.
	books, err := e.Search(ctx, src, "雾港", 1)
	if err != nil || len(books) != 2 {
		t.Fatalf("search: %v, %+v", err, books)
	}
	// Nothing says which cell of the table is the author: the book's page will.
	if b := books[0]; b.Title != "雾港钟声" || b.Author != "" || !strings.HasSuffix(b.URL, "/book/11/") {
		t.Fatalf("first hit: %+v", b)
	}

	// The home page lists each book twice and a chapter beside it; menus are no books.
	home, err := e.Recommend(ctx, src)
	if err != nil || len(home) != 8 {
		t.Fatalf("recommend: %v, %+v", err, home)
	}
	if b := home[0]; b.Title != "雾港钟声" || !strings.HasSuffix(b.Cover, "/cover/11.jpg") || b.Author != "林见" {
		t.Fatalf("first recommendation: %+v", b)
	}

	detail, err := e.Detail(ctx, src, books[0].URL)
	if err != nil {
		t.Fatal(err)
	}
	if detail.Title != "雾港钟声" || detail.Author != "林见" || !strings.HasSuffix(detail.Cover, "/cover/11.jpg") ||
		detail.Intro != "港口的钟每夜响十三下。" || detail.Latest != "第40章 归途" || detail.TocURL != books[0].URL {
		t.Fatalf("detail: %+v", detail)
	}

	// 40 chapters over two pages, in order, without the repeated newest ones or the related books.
	toc, err := e.Toc(ctx, src, detail.TocURL)
	if err != nil || len(toc) != 40 {
		t.Fatalf("toc: %v, %d chapters", err, len(toc))
	}
	for i, c := range toc {
		if want := fmt.Sprintf("/book/11/%d.html", i+1); !strings.HasSuffix(c.URL, want) {
			t.Fatalf("chapter %d is %+v, want %s", i, c, want)
		}
	}

	// Both parts of the chapter, and nothing of the next one or of the page around the text.
	c, err := e.Content(ctx, src, toc[0].URL, toc[1].URL)
	if err != nil {
		t.Fatal(err)
	}
	want := "第1章第1部分的第一段，船在雾里靠了岸。\n第二段，钟声从塔上传下来。\n最后一段。\n" +
		"第1章第2部分的第一段，船在雾里靠了岸。\n第二段，钟声从塔上传下来。\n最后一段。"
	if c.Text != want {
		t.Fatalf("content:\n%s", c.Text)
	}
}

// comicSite searches with GET, lists its chapters newest first on a page of their own, and
// loads its pictures lazily; chapter 2 lists them in a script instead.
func comicSite() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, frame, "", `<form action="/s" role="search"><input type="search" name="wd"></form>`)
	})
	mux.HandleFunc("GET /s", func(w http.ResponseWriter, r *http.Request) {
		var cards strings.Builder
		for _, b := range shelf {
			if strings.Contains(b.name, r.URL.Query().Get("wd")) {
				fmt.Fprintf(&cards, `<a class="card" href="/manga/%d"><img src="/cover/%d.jpg"><h3 class="title">%s</h3><p class="author">%s</p><p class="desc">一段足够长的漫画简介文字。</p></a>`,
					b.id, b.id, b.name, b.author)
			}
		}
		fmt.Fprintf(w, frame, "", `<div class="results">`+cards.String()+`</div>`)
	})
	mux.HandleFunc("GET /manga/{id}", func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		fmt.Fprintf(w, frame, "", fmt.Sprintf(`<div class="info"><div class="cover"><img src="/cover/%s.jpg"></div><h1>纸鸢</h1>
<span class="author">作者：沈青</span><div class="intro">线断了以后。</div>
<a href="/manga/%s/1">开始阅读</a><a href="/manga/%s/chapters">全部章节</a></div>`, id, id, id))
	})
	mux.HandleFunc("GET /manga/{id}/chapters", func(w http.ResponseWriter, r *http.Request) {
		var list strings.Builder
		for n := 8; n >= 1; n-- {
			fmt.Fprintf(&list, `<li><a href="/manga/%s/%d">第%d话</a></li>`, r.PathValue("id"), n, n)
		}
		fmt.Fprintf(w, frame, "", `<ul class="chapters">`+list.String()+`</ul>`)
	})
	mux.HandleFunc("GET /manga/{id}/{n}", func(w http.ResponseWriter, r *http.Request) {
		id, n := r.PathValue("id"), r.PathValue("n")
		var pages strings.Builder
		if n == "2" {
			fmt.Fprintf(&pages, `<div id="reader"></div><script>var pages = ["https:\/\/img.invalid\/%s\/2\/1.webp","https:\/\/img.invalid\/%s\/2\/2.webp?v=3"];</script>`, id, id)
		} else {
			for p := 1; p <= 5; p++ {
				fmt.Fprintf(&pages, `<div class="page"><img class="lazy" src="/img/loading.gif" data-src="/pages/%s/%s/%d.jpg"></div>`, id, n, p)
			}
		}
		var more strings.Builder
		for _, b := range shelf {
			fmt.Fprintf(&more, `<a href="/manga/%d"><img src="/cover/%d.jpg"></a>`, b.id, b.id)
		}
		fmt.Fprintf(w, frame, "", `<div class="reader">`+pages.String()+`</div><div class="more">`+more.String()+`</div>`)
	})
	return mux
}

func TestAComicSiteIsReadFromItsAddressAlone(t *testing.T) {
	e, src := autoSource(t, source.TypeComic, comicSite())
	ctx := context.Background()

	books, err := e.Search(ctx, src, "纸鸢", 1)
	if err != nil || len(books) != 1 {
		t.Fatalf("search: %v, %+v", err, books)
	}
	// The whole card is one link: its heading is the title, not all of its text.
	if b := books[0]; b.Title != "纸鸢" || b.Author != "沈青" || !strings.HasSuffix(b.Cover, "/cover/18.jpg") || b.Intro == "" {
		t.Fatalf("hit: %+v", b)
	}
	detail, err := e.Detail(ctx, src, books[0].URL)
	if err != nil || detail.Author != "沈青" || detail.Intro != "线断了以后。" || !strings.HasSuffix(detail.TocURL, "/manga/18/chapters") {
		t.Fatalf("detail: %v %+v", err, detail)
	}
	toc, err := e.Toc(ctx, src, detail.TocURL)
	if err != nil || len(toc) != 8 || toc[0].Title != "第1话" || toc[7].Title != "第8话" {
		t.Fatalf("toc: %v %+v", err, toc)
	}
	// The pictures are the lazily loaded ones, not their placeholder and not the covers below.
	c, err := e.Content(ctx, src, toc[0].URL, toc[1].URL)
	if err != nil || len(c.Images) != 5 || !strings.HasSuffix(c.Images[4], "/pages/18/1/5.jpg") {
		t.Fatalf("content: %v %+v", err, c)
	}
	c, err = e.Content(ctx, src, toc[1].URL, toc[2].URL)
	if err != nil || len(c.Images) != 2 || c.Images[1] != "https://img.invalid/18/2/2.webp?v=3" {
		t.Fatalf("content from a script: %v %+v", err, c)
	}
}

func TestWhatCannotBeRecognisedSaysSo(t *testing.T) {
	ctx := context.Background()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `<html><body><form action="/login"><input name="user"><input type="password" name="pw"></form><div id="app"></div></body></html>`)
	})
	mux.HandleFunc("GET /api", func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, `{"list":[]}`) })
	mux.HandleFunc("GET /empty", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `<html><body><div id="app"></div></body></html>`)
	})
	e, src := autoSource(t, source.TypeNovel, mux)

	// A login form is not a search.
	if _, err := e.Search(ctx, src, "x", 1); err == nil || !strings.Contains(err.Error(), "search.url") {
		t.Fatalf("search without a search form: %v", err)
	}
	if _, err := e.Toc(ctx, src, src.BaseURL+"/api"); err == nil || !strings.Contains(err.Error(), "JSON") {
		t.Fatalf("catalog in JSON: %v", err)
	}
	if _, err := e.Toc(ctx, src, src.BaseURL+"/empty"); err == nil || !strings.Contains(err.Error(), "toc") {
		t.Fatalf("page drawn by a script: %v", err)
	}
	if _, err := e.Content(ctx, src, src.BaseURL+"/empty", ""); err == nil || !strings.Contains(err.Error(), "content.body") {
		t.Fatalf("chapter drawn by a script: %v", err)
	}
	for _, err := range []error{mustFail(e.Toc(ctx, src, src.BaseURL+"/empty"))} {
		if source.Kind(err) != source.KindRule {
			t.Fatalf("%v is of kind %q, not a rule problem", err, source.Kind(err))
		}
	}
}

func mustFail[T any](_ T, err error) error { return err }

// A rule may leave only part of a source to recognition.
func TestAutoFillsWhatARuleLeavesOut(t *testing.T) {
	srv := httptest.NewServer(novelSite())
	t.Cleanup(srv.Close)
	src, err := source.ParseSource([]byte(`{"name":"半自动","type":"novel","baseUrl":"` + srv.URL + `",
		"search":{"url":"/search.php","method":"POST","body":"type=all&searchkey={{key}}","list":"css:table.grid tr","title":"css:td:first-child a","bookUrl":"css:td:first-child a@href","author":"css:td:last-child"},
		"content":{"body":"auto"}}`))
	if err != nil {
		t.Fatal(err)
	}
	if src.IsAuto() || src.Toc.List != source.Auto || src.Detail != (source.DetailRule{}) || src.Recommend != nil {
		t.Fatalf("filled rule: %+v", src)
	}
	e, ctx := &source.Engine{Fetcher: source.NewFetcher()}, context.Background()
	src.RateLimitMs = 1
	// The rule's own search finds books by their author, which recognition does not.
	books, err := e.Search(ctx, src, "林见", 1)
	if err != nil || len(books) != 3 || books[2].Title != "七号码头" || books[2].Author != "林见" {
		t.Fatalf("search: %v %+v", err, books)
	}
	toc, err := e.Toc(ctx, src, books[0].URL)
	if err != nil || len(toc) != 40 {
		t.Fatalf("toc: %v, %d chapters", err, len(toc))
	}
	c, err := e.Content(ctx, src, toc[0].URL, toc[1].URL)
	if err != nil || strings.Count(c.Text, "\n") != 5 {
		t.Fatalf("content: %v\n%s", err, c.Text)
	}

	for _, bad := range []string{
		`{"name":"x","type":"novel","baseUrl":"ftp://x"}`,
		`{"name":"x","type":"novel","baseUrl":"https://x.invalid","search":{"url":"/s?q={{key}}","list":"css:li"}}`,
		`{"name":"x","type":"novel","baseUrl":"https://x.invalid","toc":{"list":"css:li","url":"auto"}}`,
	} {
		if _, err := source.ParseSource([]byte(bad)); err == nil {
			t.Errorf("accepted %s", bad)
		}
	}
}

// plainSite is laid out the older way: the search form is on a page of its own, a book is
// linked from its cover and again from its name, the newest chapters stand above the catalog
// in reverse (the very newest only there), the menu is never closed, and the text of a chapter
// lies loose in the page.
func plainSite() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		var b strings.Builder
		b.WriteString(`<html><body><div class="menu"><a href="/">首页</a><a href="/find/search.php">搜索小说</a>`)
		for _, t := range shelf {
			fmt.Fprintf(&b, `<div class="item"><a href="/info/%d.html"><img src="/cover/%d.jpg" alt="%s文章列表"></a>`+
				`<span><a href="/info/%d.html">%s</a></span></div>`, t.id, t.id, t.name, t.id, t.name)
		}
		fmt.Fprint(w, b.String()+`</body></html>`)
	})
	mux.HandleFunc("GET /find/search.php", func(w http.ResponseWriter, r *http.Request) {
		key := r.URL.Query().Get("searchkey")
		var b strings.Builder
		b.WriteString(`<html><body><form action="/find/search.php"><input type="text" name="searchkey"></form><ul>`)
		for _, t := range shelf {
			if key != "" && strings.Contains(t.name, key) {
				fmt.Fprintf(&b, `<li><a href="/info/%d.html">%s</a></li>`, t.id, t.name)
			}
		}
		fmt.Fprint(w, b.String()+`</ul></body></html>`)
	})
	mux.HandleFunc("GET /info/{file}", func(w http.ResponseWriter, r *http.Request) {
		var b strings.Builder
		b.WriteString(`<html><body><div class="menu"><a href="/">首页</a><h1><i><script>count('1')</script>万字</i><a href="#dir">雾港钟声</a></h1><ul class="new">`)
		for n := 31; n >= 29; n-- {
			fmt.Fprintf(&b, `<li><a href="/read/11/%d.html">第%d章 潮汐</a></li>`, 1000+n, n)
		}
		b.WriteString(`</ul><ul id="dir">`)
		for n := 1; n <= 30; n++ {
			fmt.Fprintf(&b, `<li><a href="/read/11/%d.html">第%d章 潮汐</a></li>`, 1000+n, n)
		}
		fmt.Fprint(w, b.String()+`</ul></body></html>`)
	})
	mux.HandleFunc("GET /read/11/{file}", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `<html><body><h1><a href="/info/11.html">雾港钟声</a> 第1章 潮汐</h1>
<div class="toplink"><a href="/info/11.html">返回目录</a></div><script>frame()</script>
&nbsp;&nbsp;钟声从港口那边传过来，一共七下，比平时慢了半拍。<br><br>&nbsp;&nbsp;守塔的人放下手里的灯，往窗外看了一眼。<br>
<div class="bottom"><ul><li><a href="/shelf.php">加入书架</a></li></ul></div></body></html>`)
	})
	return mux
}

func TestAnOlderLayoutIsReadToo(t *testing.T) {
	eng, src := autoSource(t, source.TypeNovel, plainSite())
	ctx := context.Background()

	home, err := eng.Recommend(ctx, src)
	if err != nil || len(home) != len(shelf) || home[0].Title != "雾港钟声" || !strings.HasSuffix(home[0].Cover, "/cover/11.jpg") {
		t.Fatalf("home: %+v, %v", home, err)
	}
	found, err := eng.Search(ctx, src, "雾港", 1)
	if err != nil || len(found) != 2 {
		t.Fatalf("search by the form of the search page: %+v, %v", found, err)
	}
	d, err := eng.Detail(ctx, src, found[0].URL)
	if err != nil || d.Title != "雾港钟声" {
		t.Fatalf("detail: %+v, %v", d, err)
	}
	toc, err := eng.Toc(ctx, src, d.TocURL)
	if err != nil || len(toc) != 31 || !strings.HasPrefix(toc[0].Title, "第1章") || !strings.HasPrefix(toc[30].Title, "第31章") {
		t.Fatalf("toc: %d chapters, %v, first %+v", len(toc), err, toc[:min(len(toc), 1)])
	}
	c, err := eng.Content(ctx, src, toc[0].URL, toc[1].URL)
	if err != nil || !strings.Contains(c.Text, "钟声从港口") || !strings.Contains(c.Text, "守塔的人") ||
		strings.Contains(c.Text, "加入书架") || strings.Contains(c.Text, "返回目录") || strings.Contains(c.Text, "frame") {
		t.Fatalf("content: %q, %v", c.Text, err)
	}
}

func TestASourceWithSettingsOfItsOwnIsNotAuto(t *testing.T) {
	for rule, auto := range map[string]bool{
		`{"name":"x","type":"novel","baseUrl":"https://x.invalid"}`:                                      true,
		`{"name":"x","type":"comic","baseUrl":"https://x.invalid/","headers":{}}`:                        true,
		`{"name":"x","type":"comic","baseUrl":"https://x.invalid","rateLimitMs":2000}`:                   false,
		`{"name":"x","type":"novel","baseUrl":"https://x.invalid","toc":{"nextPage":"css:a.next@href"}}`: false,
		`{"name":"x","type":"novel","baseUrl":"https://x.invalid","headers":{"Cookie":"a=1"}}`:           false,
		`{"name":"x","type":"novel","baseUrl":"https://x.invalid","charset":"gbk"}`:                      false,
		`{"name":"x","type":"novel","baseUrl":"https://x.invalid","detail":{"cover":"css:img@src"}}`:     false,
	} {
		src, err := source.ParseSource([]byte(rule))
		if err != nil {
			t.Fatalf("%s: %v", rule, err)
		}
		if src.IsAuto() != auto {
			t.Errorf("%s: IsAuto = %v, want %v", rule, src.IsAuto(), auto)
		}
	}
}

func TestASubscribedSourceIsOnlyReadFromThePublicInternet(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		fmt.Fprint(w, "<html><body>内网的页面</body></html>")
	}))
	defer srv.Close()
	f := source.NewFetcher()
	own := &source.Source{Name: "自己加的", Type: source.TypeNovel, BaseURL: srv.URL, RateLimitMs: 1}
	if _, _, err := f.Page(context.Background(), own, "", srv.URL+"/", ""); err != nil || hits.Load() != 1 {
		t.Fatalf("a source added by hand: %v after %d requests", err, hits.Load())
	}
	listed := *own
	listed.Public = true
	_, _, err := f.Page(context.Background(), &listed, "", srv.URL+"/", "")
	var re source.RuleError
	if !errors.As(err, &re) || !strings.Contains(string(re), "公网") || hits.Load() != 1 {
		t.Fatalf("a source of a subscribed list: %v after %d requests, want it refused", err, hits.Load())
	}
}

func TestWhatIsLearntAboutASiteIsNotUsedWithOtherHeaders(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		action := "/search"
		if r.Header.Get("Cookie") != "" {
			action = "/member/search"
		}
		fmt.Fprintf(w, frame, "", `<form action="`+action+`" method="get"><input type="text" name="q"><button>搜索</button></form>`)
	}))
	defer srv.Close()
	e := &source.Engine{Fetcher: source.NewFetcher()}
	for _, headers := range []map[string]string{nil, {"Cookie": "member=1"}, nil} {
		src := &source.Source{Name: "同一个地址", Type: source.TypeNovel, BaseURL: srv.URL, Headers: headers, RateLimitMs: 1}
		if err := src.Validate(); err != nil {
			t.Fatal(err)
		}
		want := srv.URL + "/search?q={{key}}"
		if headers != nil {
			want = srv.URL + "/member/search?q={{key}}"
		}
		if got, err := e.SearchURL(context.Background(), src); err != nil || got != want {
			t.Errorf("headers %v: search at %q (%v), want %q", headers, got, err, want)
		}
	}
}
