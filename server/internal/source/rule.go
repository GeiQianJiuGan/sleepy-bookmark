// Package source turns a declarative JSON rule into search / detail / toc / content scraping.
package source

import (
	"encoding/json"
	"errors"
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
//
// Field rules of a list are evaluated relative to each list item; an empty expr (e.g. "@href") is the item itself.
type Source struct {
	Name    string            `json:"name"`
	Type    string            `json:"type"`
	BaseURL string            `json:"baseUrl"`
	Headers map[string]string `json:"headers,omitempty"`
	// Charset forces the page encoding (e.g. "gbk"); empty means auto-detect.
	Charset     string     `json:"charset,omitempty"`
	RateLimitMs int        `json:"rateLimitMs,omitempty"`
	Search      SearchRule `json:"search"`
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
	case s.Search.URL == "" || s.Search.List == "" || s.Search.Title == "" || s.Search.URLRule == "":
		return errors.New("search 需要 url / list / title / bookUrl")
	case s.Recommend != nil && (s.Recommend.URL == "" || s.Recommend.List == "" || s.Recommend.Title == "" || s.Recommend.URLRule == ""):
		return errors.New("recommend 需要 url / list / title / bookUrl")
	case s.Toc.List == "" || s.Toc.URL == "":
		return errors.New("toc 需要 list / url")
	case s.Type == TypeNovel && s.Content.Body == "":
		return errors.New("小说源需要 content.body")
	case s.Type == TypeComic && s.Content.Images == "":
		return errors.New("漫画源需要 content.images")
	}
	return nil
}
