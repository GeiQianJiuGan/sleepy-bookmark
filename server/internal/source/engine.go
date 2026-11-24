package source

import (
	"context"
	"fmt"
	"html"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"

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

type Engine struct{ Fetcher *Fetcher }

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
	fill := strings.NewReplacer("{{key}}", url.QueryEscape(encodeKey(key, src.Charset)), "{{page}}", strconv.Itoa(page))
	return e.list(ctx, src, src.Search, fill)
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
	return e.list(ctx, src, *src.Recommend, strings.NewReplacer("{{page}}", "1"))
}

func (e *Engine) list(ctx context.Context, src *Source, r ListRule, fill *strings.Replacer) ([]BookInfo, error) {
	// A list URL is appended to baseUrl (which may have a path) unless it is absolute.
	u := fill.Replace(r.URL)
	if !strings.HasPrefix(u, "http://") && !strings.HasPrefix(u, "https://") {
		u = src.BaseURL + "/" + strings.TrimPrefix(u, "/")
	}
	doc, u, err := e.load(ctx, src, r.Method, u, fill.Replace(r.Body))
	if err != nil {
		return nil, err
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
	b.Title = first(doc, r.Title)
	b.Author = first(doc, r.Author)
	b.Cover = resolve(base, first(doc, r.Cover))
	b.Intro = first(doc, r.Intro)
	b.Latest = first(doc, r.Latest)
	if toc := first(doc, r.TocURL); toc != "" {
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
		return nil, RuleError("目录为空，请检查 toc 规则")
	}
	return out, nil
}

func (e *Engine) toc(ctx context.Context, src *Source, rule TocRule, tocURL string) ([]ChapterInfo, error) {
	var out []ChapterInfo
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
		for _, item := range doc.Find(rule.List) {
			c := ChapterInfo{Title: first(item, rule.Name), URL: resolve(base, first(item, rule.URL))}
			if c.URL != "" {
				out = append(out, c)
			}
		}
		u = resolve(base, first(doc, rule.NextPage))
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
		if src.Type == TypeComic {
			page := doc.Values(src.Content.Images)
			for i, img := range page {
				page[i] = resolve(base, img)
			}
			c.Images = append(c.Images, page[overlap(c.Images, page):]...)
		} else {
			body := src.Content.Body
			if _, isHTML := doc.(htmlNode); isHTML {
				body = bodyRule(body)
			}
			text = append(text, doc.Values(body)...)
		}
		u = resolve(base, first(doc, src.Content.NextPage))
	}
	if src.Type == TypeComic {
		if len(c.Images) == 0 {
			return c, RuleError("没有解析到图片，请检查 content.images 规则")
		}
		return c, nil
	}
	c.Text = cleanText(strings.Join(text, "\n"), src.Content.Purify)
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
