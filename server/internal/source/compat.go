package source

import (
	"encoding/json"
	"errors"
	"regexp"
	"strings"
)

// legadoSource is the subset of Legado (阅读) book-source JSON fields that can be mapped to
// sleepy-bookmark's declarative rule engine without executing JavaScript.
type legadoSource struct {
	BookSourceName string `json:"bookSourceName"`
	BookSourceURL  string `json:"bookSourceUrl"`
	BookSourceType int    `json:"bookSourceType"` // 0=novel, 1=audio, 2=comic, 3=file
	Header         string `json:"header"`
	SearchURL      string `json:"searchUrl"`
	ExploreURL     string `json:"exploreUrl"`
	RuleSearch     *struct {
		BookList    string `json:"bookList"`
		Name        string `json:"name"`
		Author      string `json:"author"`
		BookURL     string `json:"bookUrl"`
		CoverURL    string `json:"coverUrl"`
		Intro       string `json:"intro"`
		LastChapter string `json:"lastChapter"`
	} `json:"ruleSearch"`
	RuleExplore *struct {
		BookList    string `json:"bookList"`
		Name        string `json:"name"`
		Author      string `json:"author"`
		BookURL     string `json:"bookUrl"`
		CoverURL    string `json:"coverUrl"`
		Intro       string `json:"intro"`
		LastChapter string `json:"lastChapter"`
	} `json:"ruleExplore"`
	RuleBookInfo *struct {
		Name        string `json:"name"`
		Author      string `json:"author"`
		CoverURL    string `json:"coverUrl"`
		Intro       string `json:"intro"`
		LastChapter string `json:"lastChapter"`
		TocURL      string `json:"tocUrl"`
	} `json:"ruleBookInfo"`
	RuleToc *struct {
		ChapterList string `json:"chapterList"`
		ChapterName string `json:"chapterName"`
		ChapterURL  string `json:"chapterUrl"`
		NextTocURL  string `json:"nextTocUrl"`
	} `json:"ruleToc"`
	RuleContent *struct {
		Content        string `json:"content"`
		NextContentURL string `json:"nextContentUrl"`
		ReplaceRegex   string `json:"replaceRegex"`
	} `json:"ruleContent"`
}

// ParseSourceFlexible parses a native sleepy-bookmark rule, and falls back to converting a
// declarative Legado 3.0 (nested) or Legado 2.0 / 异次元 (flat) JSON rule without JavaScript.
func ParseSourceFlexible(raw []byte) (*Source, error) {
	src, err := ParseSource(raw)
	if err == nil {
		return src, nil
	}
	if converted, matched, convErr := convertLegado(raw); matched {
		return converted, convErr
	}
	if converted, matched, convErr := convertLegadoFlat(raw); matched {
		return converted, convErr
	}
	return src, err
}

func convertLegado(raw []byte) (*Source, bool, error) {
	var l legadoSource
	if err := json.Unmarshal(raw, &l); err != nil {
		return nil, false, nil
	}
	l.BookSourceName = strings.TrimSpace(l.BookSourceName)
	l.BookSourceURL = strings.TrimRight(strings.TrimSpace(l.BookSourceURL), "/")
	hasRules := (l.RuleSearch != nil && strings.TrimSpace(l.RuleSearch.BookList) != "") ||
		(l.RuleToc != nil && strings.TrimSpace(l.RuleToc.ChapterList) != "") ||
		(l.RuleContent != nil && strings.TrimSpace(l.RuleContent.Content) != "")
	if l.BookSourceName == "" || (!strings.HasPrefix(l.BookSourceURL, "http://") && !strings.HasPrefix(l.BookSourceURL, "https://")) || !hasRules {
		return nil, false, nil
	}

	var typ string
	switch l.BookSourceType {
	case 0:
		typ = TypeNovel
	case 2:
		typ = TypeComic
	default:
		return nil, true, errors.New("仅支持小说和漫画类型的阅读书源")
	}

	if hasLegadoJS(l.SearchURL) {
		return nil, true, errors.New("搜索规则包含 JS 脚本，规则引擎不支持执行 JavaScript")
	}
	src := &Source{
		Name:    l.BookSourceName,
		Type:    typ,
		BaseURL: l.BookSourceURL,
	}
	if strings.TrimSpace(l.Header) != "" && !hasLegadoJS(l.Header) {
		var hdr map[string]string
		if json.Unmarshal([]byte(l.Header), &hdr) == nil && len(hdr) > 0 {
			src.Headers = hdr
		}
	}

	if su := strings.TrimSpace(l.SearchURL); su != "" {
		urlPart, optPart := splitLegadoURL(su)
		src.Search.URL = urlPart
		if optPart != "" {
			var opt struct {
				Method  string            `json:"method"`
				Body    string            `json:"body"`
				Charset string            `json:"charset"`
				Headers map[string]string `json:"headers"`
			}
			if json.Unmarshal([]byte(optPart), &opt) == nil {
				src.Search.Method = strings.ToUpper(strings.TrimSpace(opt.Method))
				src.Search.Body = opt.Body
				if opt.Charset != "" {
					src.Charset = strings.ToLower(strings.TrimSpace(opt.Charset))
				}
				if len(opt.Headers) > 0 {
					if src.Headers == nil {
						src.Headers = map[string]string{}
					}
					for k, v := range opt.Headers {
						src.Headers[k] = v
					}
				}
			}
		}
	}

	if rs := l.RuleSearch; rs != nil && strings.TrimSpace(rs.BookList) != "" {
		if hasLegadoJS(rs.BookList, rs.Name, rs.BookURL) {
			return nil, true, errors.New("搜索解析规则包含 JS 脚本，规则引擎不支持执行 JavaScript")
		}
		src.Search.List = convertLegadoExpr(rs.BookList, true)
		src.Search.Title = convertLegadoExpr(rs.Name, false)
		src.Search.URLRule = convertLegadoExpr(rs.BookURL, false)
		src.Search.Author = convertLegadoExpr(rs.Author, false)
		src.Search.Cover = convertLegadoExpr(rs.CoverURL, false)
		src.Search.Intro = convertLegadoExpr(rs.Intro, false)
		src.Search.Latest = convertLegadoExpr(rs.LastChapter, false)
	}

	if re := l.RuleExplore; re != nil && strings.TrimSpace(re.BookList) != "" && strings.TrimSpace(l.ExploreURL) != "" {
		if firstURL := firstLegadoExploreURL(l.ExploreURL); firstURL != "" && !hasLegadoJS(firstURL, re.BookList, re.Name, re.BookURL) {
			src.Recommend = &ListRule{
				URL:     firstURL,
				List:    convertLegadoExpr(re.BookList, true),
				Title:   convertLegadoExpr(re.Name, false),
				URLRule: convertLegadoExpr(re.BookURL, false),
				Author:  convertLegadoExpr(re.Author, false),
				Cover:   convertLegadoExpr(re.CoverURL, false),
				Intro:   convertLegadoExpr(re.Intro, false),
				Latest:  convertLegadoExpr(re.LastChapter, false),
			}
		}
	}

	if rb := l.RuleBookInfo; rb != nil {
		if !hasLegadoJS(rb.Name, rb.Author, rb.CoverURL, rb.Intro, rb.LastChapter, rb.TocURL) {
			src.Detail = DetailRule{
				Title:  convertLegadoExpr(rb.Name, false),
				Author: convertLegadoExpr(rb.Author, false),
				Cover:  convertLegadoExpr(rb.CoverURL, false),
				Intro:  convertLegadoExpr(rb.Intro, false),
				Latest: convertLegadoExpr(rb.LastChapter, false),
				TocURL: convertLegadoExpr(rb.TocURL, false),
			}
		}
	}

	if rt := l.RuleToc; rt != nil && strings.TrimSpace(rt.ChapterList) != "" {
		if hasLegadoJS(rt.ChapterList, rt.ChapterName, rt.ChapterURL) {
			return nil, true, errors.New("目录规则包含 JS 脚本，规则引擎不支持执行 JavaScript")
		}
		listRule := strings.TrimSpace(rt.ChapterList)
		if strings.HasPrefix(listRule, "-") {
			src.Toc.Reverse = true
			listRule = strings.TrimPrefix(listRule, "-")
		}
		src.Toc.List = convertLegadoExpr(listRule, true)
		src.Toc.Name = convertLegadoExpr(rt.ChapterName, false)
		src.Toc.URL = convertLegadoExpr(rt.ChapterURL, false)
		if !hasLegadoJS(rt.NextTocURL) {
			src.Toc.NextPage = convertLegadoExpr(rt.NextTocURL, false)
		}
	}

	if rc := l.RuleContent; rc != nil && strings.TrimSpace(rc.Content) != "" {
		if hasLegadoJS(rc.Content) {
			return nil, true, errors.New("正文规则包含 JS 脚本，规则引擎不支持执行 JavaScript")
		}
		expr := convertLegadoExpr(rc.Content, false)
		if typ == TypeComic {
			src.Content.Images = expr
		} else {
			src.Content.Body = expr
		}
		if !hasLegadoJS(rc.NextContentURL) {
			src.Content.NextPage = convertLegadoExpr(rc.NextContentURL, false)
		}
		if strings.TrimSpace(rc.ReplaceRegex) != "" {
			for _, line := range strings.Split(rc.ReplaceRegex, "\n") {
				line = strings.TrimSpace(line)
				if strings.HasPrefix(line, "##") {
					if parts := strings.Split(line[2:], "##"); len(parts) > 0 && parts[0] != "" {
						src.Content.Purify = append(src.Content.Purify, parts[0])
					}
				}
			}
		}
	}

	if err := src.Validate(); err != nil {
		return nil, true, err
	}
	return src, true, nil
}

func hasLegadoJS(rules ...string) bool {
	for _, r := range rules {
		low := strings.ToLower(r)
		if strings.Contains(low, "<js>") || strings.Contains(low, "@js:") || strings.Contains(low, "java.") {
			return true
		}
	}
	return false
}

func splitLegadoURL(s string) (string, string) {
	idx := strings.Index(s, ",{")
	if idx < 0 {
		return s, ""
	}
	return strings.TrimSpace(s[:idx]), strings.TrimSpace(s[idx+1:])
}

func firstLegadoExploreURL(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "[") {
		var items []struct {
			URL string `json:"url"`
		}
		if json.Unmarshal([]byte(s), &items) == nil {
			for _, it := range items {
				if u := strings.TrimSpace(it.URL); u != "" {
					return u
				}
			}
		}
		return ""
	}
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if i := strings.Index(line, "::"); i >= 0 {
			if u := strings.TrimSpace(line[i+2:]); u != "" && !strings.Contains(u, "{{") {
				return u
			}
		}
	}
	return ""
}

var reLegadoIndex = regexp.MustCompile(`\.(-?\d+)$`)

// convertLegadoExpr maps common Legado rule expressions (JSONPath, XPath, @css, default Jsoup
// chain like class.item@tag.a.0@href, and ##regex##repl) into sleepy-bookmark syntax.
func convertLegadoExpr(raw string, isList bool) string {
	s := strings.TrimSpace(raw)
	if s == "" || hasLegadoJS(s) {
		return ""
	}
	// Strip Legado leading reverse/filter markers that are handled elsewhere.
	s = strings.TrimPrefix(s, "+")
	// Separate ##regex##repl suffix first so selector conversion does not touch it.
	suffix := ""
	if idx := strings.Index(s, "##"); idx >= 0 {
		suffix = s[idx:]
		s = strings.TrimSpace(s[:idx])
	}
	// When fallback alternatives are separated by || or &&, take the first branch.
	if idx := strings.Index(s, "||"); idx >= 0 {
		s = strings.TrimSpace(s[:idx])
	}
	if s == "" {
		return ""
	}
	switch {
	case strings.HasPrefix(s, "@css:"):
		return "css:" + strings.TrimSpace(s[len("@css:"):]) + suffix
	case strings.HasPrefix(s, "@XPath:") || strings.HasPrefix(s, "@xpath:"):
		return "xpath:" + strings.TrimSpace(s[len("@XPath:"):]) + suffix
	case strings.HasPrefix(s, "//") || strings.HasPrefix(s, "./"):
		return "xpath:" + s + suffix
	case strings.HasPrefix(s, "$."):
		return "json:" + jsonPathToGJSON(strings.TrimPrefix(s, "$.")) + suffix
	case strings.HasPrefix(s, "@json:"):
		return "json:" + jsonPathToGJSON(strings.TrimPrefix(strings.TrimSpace(s[len("@json:"):]), "$.")) + suffix
	case strings.HasPrefix(s, ":") && len(s) > 1:
		return "regex:" + s[1:] + suffix
	}

	// Default Legado Jsoup chain: segment1@segment2@attr
	parts := strings.Split(s, "@")
	var selectors []string
	attr := ""
	for i, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		isLast := i == len(parts)-1
		if isLast && !isList && isLegadoAttr(p) {
			switch p {
			case "text", "textNodes", "ownText":
				attr = "text"
			case "html":
				attr = "html"
			case "all":
				attr = "outerHtml"
			default:
				attr = p
			}
			continue
		}
		selectors = append(selectors, legadoSegmentToCSS(p))
	}
	sel := strings.Join(selectors, " ")
	if sel == "" && attr == "" {
		return ""
	}
	out := "css:" + sel
	if attr != "" && !isList {
		out += "@" + attr
	}
	return out + suffix
}

func isLegadoAttr(s string) bool {
	if strings.ContainsAny(s, ".#[] >") {
		return false
	}
	switch s {
	case "text", "textNodes", "ownText", "html", "all", "href", "src", "data-src", "data-original", "alt", "title", "content":
		return true
	}
	return strings.HasPrefix(s, "data-")
}

func legadoSegmentToCSS(seg string) string {
	// Strip trailing positional index like .0 or .-1 (CSS uses the selector broadly; first() takes index 0).
	seg = reLegadoIndex.ReplaceAllString(seg, "")
	switch {
	case strings.HasPrefix(seg, "class."):
		cls := strings.TrimPrefix(seg, "class.")
		cls = strings.Join(strings.Fields(cls), ".")
		return "." + cls
	case strings.HasPrefix(seg, "id."):
		return "#" + strings.TrimPrefix(seg, "id.")
	case strings.HasPrefix(seg, "tag."):
		return strings.TrimPrefix(seg, "tag.")
	default:
		return seg
	}
}

func jsonPathToGJSON(p string) string {
	p = strings.TrimSuffix(p, "[*]")
	p = strings.ReplaceAll(p, "[*].", ".#.")
	p = strings.ReplaceAll(p, "[*]", ".#")
	return p
}

// legadoFlatSource covers Legado 2.0 and Yiciyuan (异次元) flat-field comic/novel JSON rules.
type legadoFlatSource struct {
	BookSourceName     string `json:"bookSourceName"`
	BookSourceURL      string `json:"bookSourceUrl"`
	BookSourceType     any    `json:"bookSourceType"` // "漫画", "IMAGE", 2, or ""/0 for novel
	HTTPUserAgent      string `json:"httpUserAgent"`
	RuleSearchURL      string `json:"ruleSearchUrl"`
	RuleSearchList     string `json:"ruleSearchList"`
	RuleSearchName     string `json:"ruleSearchName"`
	RuleSearchAuthor   string `json:"ruleSearchAuthor"`
	RuleSearchNoteURL  string `json:"ruleSearchNoteUrl"`
	RuleSearchCoverURL string `json:"ruleSearchCoverUrl"`
	RuleBookName       string `json:"ruleBookName"`
	RuleBookAuthor     string `json:"ruleBookAuthor"`
	RuleCoverURL       string `json:"ruleCoverUrl"`
	RuleIntroduce      string `json:"ruleIntroduce"`
	RuleChapterURL     string `json:"ruleChapterUrl"`
	RuleChapterList    string `json:"ruleChapterList"`
	RuleChapterName    string `json:"ruleChapterName"`
	RuleContentURL     string `json:"ruleContentUrl"`
	RuleBookContent    string `json:"ruleBookContent"`
}

func convertLegadoFlat(raw []byte) (*Source, bool, error) {
	var l legadoFlatSource
	if err := json.Unmarshal(raw, &l); err != nil {
		return nil, false, nil
	}
	l.BookSourceName = strings.TrimSpace(l.BookSourceName)
	burl := strings.TrimSpace(l.BookSourceURL)
	if i := strings.IndexByte(burl, '#'); i >= 0 {
		burl = burl[:i]
	}
	burl = strings.TrimRight(burl, "/")
	hasRules := strings.TrimSpace(l.RuleSearchList) != "" || strings.TrimSpace(l.RuleChapterList) != "" || strings.TrimSpace(l.RuleBookContent) != ""
	if l.BookSourceName == "" || (!strings.HasPrefix(burl, "http://") && !strings.HasPrefix(burl, "https://")) || !hasRules {
		return nil, false, nil
	}

	typ := TypeNovel
	switch v := l.BookSourceType.(type) {
	case float64:
		if int(v) == 2 {
			typ = TypeComic
		} else if int(v) != 0 {
			return nil, true, errors.New("仅支持小说和漫画类型的书源")
		}
	case string:
		low := strings.ToLower(strings.TrimSpace(v))
		if strings.Contains(low, "漫画") || strings.Contains(low, "comic") || strings.Contains(low, "image") {
			typ = TypeComic
		}
	}

	su := strings.TrimSpace(l.RuleSearchURL)
	su = strings.ReplaceAll(su, "searchKey", "{{key}}")
	su = strings.ReplaceAll(su, "searchPage", "{{page}}")
	if strings.Contains(l.RuleBookContent, "_H006_") || strings.Contains(l.RuleChapterList, "_H006_") {
		return nil, true, errors.New("该图源使用了异次元加密规则，无法直接解析")
	}
	if hasLegadoJS(su, l.RuleSearchList, l.RuleSearchName, l.RuleSearchNoteURL, l.RuleChapterList, l.RuleChapterName, l.RuleContentURL, l.RuleBookContent) {
		return nil, true, errors.New("规则包含 JS 脚本，规则引擎不支持执行 JavaScript")
	}

	src := &Source{Name: l.BookSourceName, Type: typ, BaseURL: burl}
	if ua := strings.TrimSpace(l.HTTPUserAgent); ua != "" && !hasLegadoJS(ua) {
		src.Headers = map[string]string{"User-Agent": ua}
	}

	if su != "" {
		if i := strings.Index(su, "|char="); i >= 0 {
			src.Charset = strings.ToLower(strings.TrimSpace(su[i+len("|char="):]))
			su = strings.TrimSpace(su[:i])
		}
		if i := strings.IndexByte(su, '@'); i >= 0 {
			after := su[i+1:]
			if strings.Contains(after, "=") && !strings.HasPrefix(after, "http") {
				src.Search.Method = "POST"
				src.Search.Body = after
				su = strings.TrimSpace(su[:i])
			}
		}
		src.Search.URL = su
	}
	if strings.TrimSpace(l.RuleSearchList) != "" {
		src.Search.List = convertLegadoExpr(l.RuleSearchList, true)
		src.Search.Title = convertLegadoExpr(l.RuleSearchName, false)
		src.Search.URLRule = convertLegadoExpr(l.RuleSearchNoteURL, false)
		src.Search.Author = convertLegadoExpr(l.RuleSearchAuthor, false)
		src.Search.Cover = convertLegadoExpr(l.RuleSearchCoverURL, false)
	}
	src.Detail = DetailRule{
		Title:  convertLegadoExpr(l.RuleBookName, false),
		Author: convertLegadoExpr(l.RuleBookAuthor, false),
		Cover:  convertLegadoExpr(l.RuleCoverURL, false),
		Intro:  convertLegadoExpr(l.RuleIntroduce, false),
		TocURL: convertLegadoExpr(l.RuleChapterURL, false),
	}
	if cl := strings.TrimSpace(l.RuleChapterList); cl != "" {
		if strings.HasPrefix(cl, "-") {
			src.Toc.Reverse = true
			cl = strings.TrimPrefix(cl, "-")
		}
		src.Toc.List = convertLegadoExpr(cl, true)
		src.Toc.Name = convertLegadoExpr(l.RuleChapterName, false)
		src.Toc.URL = convertLegadoExpr(l.RuleContentURL, false)
	}
	if cb := strings.TrimSpace(l.RuleBookContent); cb != "" {
		expr := convertLegadoExpr(cb, false)
		if typ == TypeComic {
			if strings.HasPrefix(expr, "css:") && !strings.Contains(expr, "@") {
				if strings.Contains(expr, "img") {
					expr += "@src"
				} else {
					expr += " img@src"
				}
			}
			src.Content.Images = expr
		} else {
			src.Content.Body = expr
		}
	}
	if err := src.Validate(); err != nil {
		return nil, true, err
	}
	return src, true, nil
}
