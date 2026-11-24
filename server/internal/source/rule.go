// Package source turns a declarative JSON rule into search / detail / toc / content scraping.
package source

import (
	"cmp"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
)

const (
	TypeNovel = "novel"
	TypeComic = "comic"
)

// Source is one configurable novel or comic site.
//
// Every rule string has the form  [kind:]expr[@attr][##regex[##replacement]]
//
//	css:.list li a@href     CSS selector, @attr is text (default) / html / outerHtml / any attribute
//	xpath://div[@id="x"]//a/@href
//	json:data.list.#.name   gjson path, the default for JSON responses
//	regex:var url = "(.*?)" first capture group of every match
//	auto                    recognised from the page, see auto.go
//
// Field rules of a list are evaluated relative to each list item; an empty expr (e.g. "@href") is the item itself.
type Source struct {
	Name    string            `json:"name"`
	Type    string            `json:"type"`
	BaseURL string            `json:"baseUrl"`
	Headers map[string]string `json:"headers,omitempty"`
	// Charset forces the page encoding (e.g. "gbk"); empty means auto-detect.
	Charset     string `json:"charset,omitempty"`
	RateLimitMs int    `json:"rateLimitMs,omitempty"`
	// Public is set on a source of a subscribed list, which is read from public addresses only.
	Public bool `json:"-"`

	Search SearchRule `json:"search"`
	// Recommend is an optional book list page (ranking, home page) shown before a search.
	Recommend *ListRule   `json:"recommend,omitempty"`
	Detail    DetailRule  `json:"detail"`
	Toc       TocRule     `json:"toc"`
	Content   ContentRule `json:"content"`
}

// ListRule reads books from a list page. The search uses it with {{key}} and {{page}} in URL and Body.
type ListRule struct {
	URL     string `json:"url"`
	Method  string `json:"method,omitempty"`
	Body    string `json:"body,omitempty"`
	List    string `json:"list"`
	Title   string `json:"title"`
	URLRule string `json:"bookUrl"`
	Author  string `json:"author,omitempty"`
	Cover   string `json:"cover,omitempty"`
	Intro   string `json:"intro,omitempty"`
	Latest  string `json:"latest,omitempty"`
}

type SearchRule = ListRule

type DetailRule struct {
	Title  string `json:"title,omitempty"`
	Author string `json:"author,omitempty"`
	Cover  string `json:"cover,omitempty"`
	Intro  string `json:"intro,omitempty"`
	Latest string `json:"latest,omitempty"`
	// TocURL is the chapter list page; empty means the detail page itself.
	TocURL string `json:"tocUrl,omitempty"`
}

type TocRule struct {
	List     string `json:"list"`
	Name     string `json:"name"`
	URL      string `json:"url"`
	NextPage string `json:"nextPage,omitempty"`
	Reverse  bool   `json:"reverse,omitempty"`
	// Fallback is tried when the rule finds no chapter: some sites lay out a short catalog
	// differently from a long one.
	Fallback *TocRule `json:"fallback,omitempty"`
}

type ContentRule struct {
	// Body is the chapter text of a novel source.
	Body string `json:"body,omitempty"`
	// Images is the picture URL list of a comic source.
	Images   string `json:"images,omitempty"`
	NextPage string `json:"nextPage,omitempty"`
	// Purify lists regular expressions removed from the chapter text.
	Purify []string `json:"purify,omitempty"`
}

func ParseSource(raw []byte) (*Source, error) {
	var s Source
	if err := json.Unmarshal(raw, &s); err != nil {
		return nil, err
	}
	return &s, s.Validate()
}

func (s *Source) Validate() error {
	s.Name = strings.TrimSpace(s.Name)
	s.BaseURL = strings.TrimRight(strings.TrimSpace(s.BaseURL), "/")
	switch {
	case s.Name == "":
		return errors.New("name 不能为空")
	case s.Type != TypeNovel && s.Type != TypeComic:
		return errors.New("type 必须是 novel 或 comic")
	case !strings.HasPrefix(s.BaseURL, "http://") && !strings.HasPrefix(s.BaseURL, "https://"):
		return errors.New("baseUrl 必须以 http:// 或 https:// 开头")
	}
	s.fillAuto()
	switch {
	case s.Search.List == "" || s.Recommend != nil && s.Recommend.List == "":
		return errors.New("写了 title / bookUrl 的列表需要 list")
	case s.Toc.List == "":
		return errors.New("写了 name / url 的 toc 需要 list")
	case !isAuto(s.Search.List) && (missing(s.Search.Title) || missing(s.Search.URLRule)):
		return errors.New("search.list 不是 auto 时需要 title / bookUrl")
	case s.Recommend != nil && !isAuto(s.Recommend.List) && (missing(s.Recommend.Title) || missing(s.Recommend.URLRule)):
		return errors.New("recommend.list 不是 auto 时需要 title / bookUrl")
	case !isAuto(s.Toc.List) && missing(s.Toc.URL):
		return errors.New("toc.list 不是 auto 时需要 url")
	case s.Toc.Fallback != nil && (s.Toc.Fallback.List == "" || s.Toc.Fallback.URL == ""):
		return errors.New("toc.fallback 需要 list / url")
	}
	return nil
}

// missing reports whether a field that a hand-written list needs is left out.
func missing(rule string) bool { return rule == "" || isAuto(rule) }

// fillAuto leaves to recognition what a rule does not say. A source of nothing but a name, a
// type and an address is recognised in full, with its details and its home page as the list of
// recommendations; in any other, the parts that a source cannot do without are.
func (s *Source) fillAuto() {
	c := &s.Content
	if s.Search == (ListRule{}) && s.Recommend == nil && s.Detail == (DetailRule{}) && s.Toc == (TocRule{}) &&
		c.Body == "" && c.Images == "" && c.NextPage == "" && len(c.Purify) == 0 {
		s.Recommend = &ListRule{}
		s.Detail = DetailRule{Auto, Auto, Auto, Auto, Auto, Auto}
	}
	list := func(r *ListRule) {
		if r.URL == "" || isAuto(r.URL) {
			r.URL = Auto
		}
		if isAuto(r.List) || r.List == "" && missing(r.Title) && missing(r.URLRule) {
			r.List, r.Title, r.URLRule = Auto, Auto, Auto
		}
	}
	list(&s.Search)
	if s.Recommend != nil {
		list(s.Recommend)
	}
	// What is recognised is followed over its pages too, unless the rule says how.
	if isAuto(s.Toc.List) || s.Toc.List == "" && missing(s.Toc.Name) && missing(s.Toc.URL) {
		s.Toc.List, s.Toc.Name, s.Toc.URL = Auto, Auto, Auto
		s.Toc.NextPage = cmp.Or(s.Toc.NextPage, Auto)
	}
	if s.Type == TypeNovel && (c.Body == "" || isAuto(c.Body)) {
		c.Body, c.NextPage = Auto, cmp.Or(c.NextPage, Auto)
	}
	if s.Type == TypeComic && (c.Images == "" || isAuto(c.Images)) {
		c.Images, c.NextPage = Auto, cmp.Or(c.NextPage, Auto)
	}
}

// IsAuto reports whether a source is no more than a name, a type and an address, with all it
// reads left to recognition. Headers, a charset or one rule make it a source written by hand.
func (s *Source) IsAuto() bool {
	plain := &Source{Name: s.Name, Type: s.Type, BaseURL: s.BaseURL}
	plain.fillAuto()
	own := *s
	own.Public = false
	if len(own.Headers) == 0 {
		own.Headers = nil
	}
	if len(own.Content.Purify) == 0 {
		own.Content.Purify = nil
	}
	return reflect.DeepEqual(&own, plain)
}
