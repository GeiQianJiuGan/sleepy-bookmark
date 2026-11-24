package source

import (
	"cmp"
	"context"
	"fmt"
	"html"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/PuerkitoBio/goquery"
	"golang.org/x/net/html/charset"
)

const (
	maxTocPages     = 100
	maxContentPages = 30
)

type BookInfo struct {
	Title  string `json:"title"`
	Author string `json:"author"`
	Cover  string `json:"cover"`
	Intro  string `json:"intro"`
	Latest string `json:"latest"`
	URL    string `json:"url"`
	TocURL string `json:"tocUrl,omitempty"`
}

type ChapterInfo struct {
	Title string `json:"title"`
	URL   string `json:"url"`
}

// Content is the payload of one chapter: Text for novels, Images for comics.
type Content struct {
	Text   string   `json:"text,omitempty"`
	Images []string `json:"images,omitempty"`
}

type Engine struct {
	Fetcher *Fetcher

	mu sync.Mutex
	// sites is what recognition has learnt about a site, by its base URL.
	sites map[string]site
}

type site struct {
	search endpoint
	at     time.Time
	// books is the kind of address that the site's search results have.
	books string
}

// searchTTL is how long the search found on a home page is used before looking again.
const searchTTL = 6 * time.Hour

const noAuto = "自动识别只支持网页（HTML），这个地址返回的是 JSON 数据，请在高级模式里写规则"

// markup gives the HTML of a parsed page to the recognition.
func markup(doc Node) (*goquery.Selection, error) {
	if h, ok := doc.(htmlNode); ok {
		return h.sel, nil
	}
	return nil, RuleError(noAuto)
}

// searchEndpoint is the search of a site whose rule leaves it to be found on the home page.
func (e *Engine) searchEndpoint(ctx context.Context, src *Source) (endpoint, error) {
	e.mu.Lock()
	known, ok := e.sites[src.BaseURL]
	e.mu.Unlock()
	if ok && known.search.url != "" && time.Since(known.at) < searchTTL {
		return known.search, nil
	}
	doc, base, err := e.load(ctx, src, "", src.BaseURL+"/", "")
	if err != nil {
		return endpoint{}, err
	}
	sel, err := markup(doc)
	if err != nil {
		return endpoint{}, err
	}
	ep, ok := autoSearch(sel, base)
	if next := searchPage(sel, base); !ok && next != "" {
		if doc, base, err = e.load(ctx, src, "", next, ""); err != nil {
			return endpoint{}, err
		}
		if sel, err = markup(doc); err != nil {
			return endpoint{}, err
		}
		ep, ok = autoSearch(sel, base)
	}
	if !ok {
		return ep, RuleError("没有在首页找到搜索框，识别不了搜索地址，请在高级模式里填写 search.url")
	}
	e.learn(src, func(s *site) { s.search, s.at = ep, time.Now() })
	return ep, nil
}

func (e *Engine) learn(src *Source, set func(*site)) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.sites == nil {
		e.sites = map[string]site{}
	}
	s := e.sites[src.BaseURL]
	set(&s)
	e.sites[src.BaseURL] = s
}

// SearchURL is where a source sends its searches, with {{key}} for the keyword.
func (e *Engine) SearchURL(ctx context.Context, src *Source) (string, error) {
	if !isAuto(src.Search.URL) {
		return src.Search.URL, nil
	}
	ep, err := e.searchEndpoint(ctx, src)
	return ep.url, err
}

// load returns the parsed page and its final URL after redirects; relative links in the page
// must be resolved against that URL rather than the requested one.
func (e *Engine) load(ctx context.Context, src *Source, method, u, body string) (Node, string, error) {
	data, final, err := e.Fetcher.Page(ctx, src, method, u, body)
	if err != nil {
		return nil, "", err
	}
	doc, err := parseDoc(data)
	return doc, final, err
}

func (e *Engine) Search(ctx context.Context, src *Source, key string, page int) ([]BookInfo, error) {
	if page < 1 {
		page = 1
	}
	r, enc := src.Search, src.Charset
	if isAuto(r.URL) {
		// A search found on the home page has one page of results.
		if page > 1 {
			return nil, nil
		}
		ep, err := e.searchEndpoint(ctx, src)
		if err != nil {
			return nil, err
		}
		r.URL, r.Method, r.Body = ep.url, ep.method, ep.body
		enc = cmp.Or(enc, ep.charset)
	}
	fill := strings.NewReplacer("{{key}}", url.QueryEscape(encodeKey(key, enc)), "{{page}}", strconv.Itoa(page))
	return e.list(ctx, src, r, fill, key)
}

// encodeKey puts the search keyword into the charset the site is forced to: a GBK site reads
// its query string and form as GBK too.
func encodeKey(key, label string) string {
	if label == "" {
		return key
	}
	enc, _ := charset.Lookup(label)
	if enc == nil {
		return key
	}
	if out, err := enc.NewEncoder().String(key); err == nil {
		return out
	}
	return key
}

// Recommend reads the source's recommendation list; a source without one has none.
func (e *Engine) Recommend(ctx context.Context, src *Source) ([]BookInfo, error) {
	if src.Recommend == nil {
		return nil, nil
	}
	r := *src.Recommend
	if isAuto(r.URL) {
		r.URL = "/"
	}
	return e.list(ctx, src, r, strings.NewReplacer("{{page}}", "1"), "")
}

// list reads the books of a list page; key is the keyword that the page is a search for.
func (e *Engine) list(ctx context.Context, src *Source, r ListRule, fill *strings.Replacer, key string) ([]BookInfo, error) {
	// A list URL is appended to baseUrl (which may have a path) unless it is absolute.
	u := fill.Replace(r.URL)
	if !strings.HasPrefix(u, "http://") && !strings.HasPrefix(u, "https://") {
		u = src.BaseURL + "/" + strings.TrimPrefix(u, "/")
	}
	asked := u
	doc, u, err := e.load(ctx, src, r.Method, u, fill.Replace(r.Body))
	if err != nil {
		return nil, err
	}
	if isAuto(r.List) {
		sel, err := markup(doc)
		if err != nil {
			return nil, err
		}
		e.mu.Lock()
		known := e.sites[src.BaseURL].books
		e.mu.Unlock()
		books, kind := autoBooks(sel, u, key, known)
		if key != "" && kind != "" {
			e.learn(src, func(s *site) { s.books = kind })
		}
		// A search with a single result is often answered with that book's page.
		if title := autoDetail(sel, u, "title"); len(books) == 0 && key != "" && u != asked &&
			strings.Contains(strings.ToLower(title), strings.ToLower(strings.TrimSpace(key))) {
			books = []BookInfo{{Title: title, URL: u, Author: autoDetail(sel, u, "author"),
				Cover: autoDetail(sel, u, "cover"), Intro: autoDetail(sel, u, "intro")}}
		}
		return books, nil
	}
	var out []BookInfo
	for _, item := range doc.Find(r.List) {
		b := BookInfo{
			Title:  first(item, r.Title),
			Author: first(item, r.Author),
			Cover:  resolve(u, first(item, r.Cover)),
			Intro:  first(item, r.Intro),
			Latest: first(item, r.Latest),
			URL:    resolve(u, first(item, r.URLRule)),
		}
		if b.Title != "" && b.URL != "" {
			out = append(out, b)
		}
	}
	return out, nil
}

func (e *Engine) Detail(ctx context.Context, src *Source, bookURL string) (BookInfo, error) {
	b := BookInfo{URL: bookURL, TocURL: bookURL}
	r := src.Detail
	if r == (DetailRule{}) {
		return b, nil
	}
	doc, base, err := e.load(ctx, src, "", bookURL, "")
	if err != nil {
		return b, err
	}
	sel, _ := markup(doc)
	field := func(rule, name string) string {
		switch {
		case !isAuto(rule):
			return first(doc, rule)
		case sel != nil:
			return autoDetail(sel, base, name)
		}
		return ""
	}
	b.Title = field(r.Title, "title")
	b.Author = field(r.Author, "author")
	b.Cover = resolve(base, field(r.Cover, "cover"))
	b.Intro = field(r.Intro, "intro")
	b.Latest = field(r.Latest, "latest")
	if toc := field(r.TocURL, "tocUrl"); toc != "" {
		b.TocURL = resolve(base, toc)
	}
	return b, nil
}

// Toc reads the chapter list with the toc rule, and with its fallback when that finds nothing.
func (e *Engine) Toc(ctx context.Context, src *Source, tocURL string) ([]ChapterInfo, error) {
	out, err := e.toc(ctx, src, src.Toc, tocURL)
	if err == nil && len(out) == 0 && src.Toc.Fallback != nil {
		out, err = e.toc(ctx, src, *src.Toc.Fallback, tocURL)
	}
	if err != nil {
		return nil, err
	}
	if len(out) == 0 {
		if isAuto(src.Toc.List) {
			return nil, RuleError("没有识别到章节目录，请在高级模式里填写 toc 规则")
		}
		return nil, RuleError("目录为空，请检查 toc 规则")
	}
	return out, nil
}

func (e *Engine) toc(ctx context.Context, src *Source, rule TocRule, tocURL string) ([]ChapterInfo, error) {
	var out, newest []ChapterInfo
	seen := map[string]bool{}
	for u, n := tocURL, 0; u != "" && !seen[u]; n++ {
		if n == maxTocPages {
			return nil, RuleError(fmt.Sprintf("目录超过 %d 页，无法读完", maxTocPages))
		}
		seen[u] = true
		doc, base, err := e.load(ctx, src, "", u, "")
		if err != nil {
			return nil, err
		}
		seen[base] = true // a later link may name this page by the address it redirected to
		sel, _ := markup(doc)
		if isAuto(rule.List) {
			if sel == nil {
				return nil, RuleError(noAuto)
			}
			list, above := autoChapters(sel, base)
			out, newest = append(out, list...), append(newest, above...)
		} else {
			for _, item := range doc.Find(rule.List) {
				c := ChapterInfo{Title: first(item, rule.Name), URL: resolve(base, first(item, rule.URL))}
				if c.URL != "" {
					out = append(out, c)
				}
			}
		}
		if !isAuto(rule.NextPage) {
			u = resolve(base, first(doc, rule.NextPage))
		} else if u = ""; sel != nil {
			u = autoNextList(sel, base)
		}
	}
	if isAuto(rule.List) {
		if out = lastOf(out); newestFirst(out) {
			slices.Reverse(out)
		}
		// A chapter that only the block of the latest ones names is the newest of the book.
		for _, c := range slices.Backward(lastOf(newest)) {
			if !slices.ContainsFunc(out, func(o ChapterInfo) bool { return o.URL == c.URL }) {
				out = append(out, c)
			}
		}
	}
	if rule.Reverse {
		slices.Reverse(out)
	}
	return out, nil
}

// overlap is how many images at the start of next repeat the end of prev: sites that split a
// chapter over pages begin a page with the last images of the page before.
func overlap(prev, next []string) int {
	for n := min(len(prev), len(next)); n > 0; n-- {
		if slices.Equal(prev[len(prev)-n:], next[:n]) {
			return n
		}
	}
	return 0
}

// Content follows content.nextPage until it is empty, repeats, or reaches stopURL (the next chapter).
func (e *Engine) Content(ctx context.Context, src *Source, chapterURL, stopURL string) (Content, error) {
	var c Content
	var text []string
	seen := map[string]bool{stopURL: true}
	for u, n := chapterURL, 0; u != "" && !seen[u]; n++ {
		if n == maxContentPages {
			return c, RuleError(fmt.Sprintf("一章超过 %d 页，请检查 content.nextPage 规则", maxContentPages))
		}
		seen[u] = true
		doc, base, err := e.load(ctx, src, "", u, "")
		if err != nil {
			return c, err
		}
		// The link redirected to a page that was already read, or to the next chapter.
		if base != u && seen[base] {
			break
		}
		seen[base] = true
		sel, _ := markup(doc)
		auto := isAuto(src.Content.Body) || isAuto(src.Content.Images)
		switch {
		case auto && sel == nil:
			return c, RuleError(noAuto)
		case src.Type == TypeComic:
			read := autoImages
			if !auto {
				read = func(*goquery.Selection, string) []string { return doc.Values(src.Content.Images) }
			}
			images := read(sel, base)
			for i, img := range images {
				images[i] = resolve(base, img)
			}
			c.Images = append(c.Images, images[overlap(c.Images, images):]...)
		case auto:
			text = append(text, autoBody(sel))
		default:
			body := src.Content.Body
			if sel != nil {
				body = bodyRule(body)
			}
			text = append(text, doc.Values(body)...)
		}
		if !isAuto(src.Content.NextPage) {
			u = resolve(base, first(doc, src.Content.NextPage))
		} else if u = ""; sel != nil {
			u = autoNextPart(sel, base, chapterURL)
		}
	}
	if src.Type == TypeComic {
		switch {
		case len(c.Images) > 0:
			return c, nil
		case isAuto(src.Content.Images):
			return c, RuleError("没有识别到漫画图片，图片可能由脚本加载，请在高级模式里填写 content.images")
		}
		return c, RuleError("没有解析到图片，请检查 content.images 规则")
	}
	c.Text = cleanText(strings.Join(text, "\n"), src.Content.Purify)
	if c.Text == "" && isAuto(src.Content.Body) {
		return c, RuleError("没有识别到正文，请在高级模式里填写 content.body")
	}
	if c.Text == "" {
		return c, RuleError("正文为空，请检查 content.body 规则")
	}
	return c, nil
}

// bodyRule makes a CSS body rule return HTML by default so that paragraph breaks survive.
func bodyRule(s string) string {
	r := parseRule(s, "css")
	if r.kind == "css" && r.attr == "" && !strings.Contains(s, "##") {
		return s + "@html"
	}
	return s
}

var (
	reBreak = regexp.MustCompile(`(?i)<br\s*/?>|</p>|</div>|</h\d>`)
	reDrop  = regexp.MustCompile(`(?is)<(script|style)[^>]*>.*?</(script|style)>`)
	reTag   = regexp.MustCompile(`<[^>]+>`)
)

func cleanText(s string, purify []string) string {
	s = reDrop.ReplaceAllString(s, "")
	s = reBreak.ReplaceAllString(s, "\n")
	s = html.UnescapeString(reTag.ReplaceAllString(s, ""))
	for _, p := range purify {
		if re, err := regexp.Compile(p); err == nil {
			s = re.ReplaceAllString(s, "")
		}
	}
	var lines []string
	for _, line := range strings.Split(s, "\n") {
		if line = strings.Trim(line, " \t\r\u00a0\u3000"); line != "" {
			lines = append(lines, line)
		}
	}
	return strings.Join(lines, "\n")
}

func resolve(base, ref string) string {
	ref = strings.TrimSpace(ref)
	if ref == "" || strings.HasPrefix(ref, "javascript:") {
		return ""
	}
	b, err := url.Parse(base)
	if err != nil {
		return ref
	}
	r, err := url.Parse(ref)
	if err != nil {
		return ref
	}
	return b.ResolveReference(r).String()
}
