package source

import (
	"context"
	"errors"
	"fmt"
	"html"
	"net/url"
	"regexp"
	"strconv"
	"strings"
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
	fill := strings.NewReplacer("{{key}}", url.QueryEscape(key), "{{page}}", strconv.Itoa(page))
	// A search URL is appended to baseUrl (which may have a path) unless it is absolute.
	u := fill.Replace(src.Search.URL)
	if !strings.HasPrefix(u, "http://") && !strings.HasPrefix(u, "https://") {
		u = src.BaseURL + "/" + strings.TrimPrefix(u, "/")
	}
	doc, u, err := e.load(ctx, src, src.Search.Method, u, fill.Replace(src.Search.Body))
	if err != nil {
		return nil, err
	}
	r := src.Search
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

func (e *Engine) Toc(ctx context.Context, src *Source, tocURL string) ([]ChapterInfo, error) {
	var out []ChapterInfo
	seen := map[string]bool{}
	for u, n := tocURL, 0; u != "" && !seen[u]; n++ {
		if n == maxTocPages {
			return nil, fmt.Errorf("目录超过 %d 页，无法读完", maxTocPages)
		}
		seen[u] = true
		doc, base, err := e.load(ctx, src, "", u, "")
		if err != nil {
			return nil, err
		}
		seen[base] = true // a later link may name this page by the address it redirected to
		for _, item := range doc.Find(src.Toc.List) {
			c := ChapterInfo{Title: first(item, src.Toc.Name), URL: resolve(base, first(item, src.Toc.URL))}
			if c.URL != "" {
				out = append(out, c)
			}
		}
		u = resolve(base, first(doc, src.Toc.NextPage))
	}
	if len(out) == 0 {
		return nil, errors.New("目录为空，请检查 toc 规则")
	}
	if src.Toc.Reverse {
		for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
			out[i], out[j] = out[j], out[i]
		}
	}
	return out, nil
}

// Content follows content.nextPage until it is empty, repeats, or reaches stopURL (the next chapter).
func (e *Engine) Content(ctx context.Context, src *Source, chapterURL, stopURL string) (Content, error) {
	var c Content
	var text []string
	seen := map[string]bool{stopURL: true}
	for u, n := chapterURL, 0; u != "" && !seen[u]; n++ {
		if n == maxContentPages {
			return c, fmt.Errorf("一章超过 %d 页，请检查 content.nextPage 规则", maxContentPages)
		}
		seen[u] = true
		doc, base, err := e.load(ctx, src, "", u, "")
		if err != nil {
			return c, err
		}
		seen[base] = true
		if src.Type == TypeComic {
			for _, img := range doc.Values(src.Content.Images) {
				c.Images = append(c.Images, resolve(base, img))
			}
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
			return c, errors.New("没有解析到图片，请检查 content.images 规则")
		}
		return c, nil
	}
	c.Text = cleanText(strings.Join(text, "\n"), src.Content.Purify)
	if c.Text == "" {
		return c, errors.New("正文为空，请检查 content.body 规则")
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
