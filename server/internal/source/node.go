package source

import (
	"bytes"
	"regexp"
	"strings"

	"github.com/PuerkitoBio/goquery"
	"github.com/antchfx/htmlquery"
	"github.com/tidwall/gjson"
)

// Node is a parsed response (or a fragment of it) that rules are evaluated against.
type Node interface {
	Find(rule string) []Node
	Values(rule string) []string
}

func first(n Node, rule string) string {
	if rule == "" {
		return ""
	}
	if v := n.Values(rule); len(v) > 0 {
		return v[0]
	}
	return ""
}

func parseDoc(body []byte) (Node, error) {
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) > 0 && (trimmed[0] == '{' || trimmed[0] == '[') && gjson.ValidBytes(trimmed) {
		return jsonNode{gjson.ParseBytes(trimmed)}, nil
	}
	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	return htmlNode{doc.Selection}, nil
}

type rule struct {
	kind, expr, attr string
	re               *regexp.Regexp
	repl             string
}

func parseRule(s, defaultKind string) rule {
	r := rule{kind: defaultKind}
	if parts := strings.SplitN(s, "##", 3); len(parts) > 1 {
		s = parts[0]
		r.re, _ = regexp.Compile(parts[1])
		if len(parts) == 3 {
			r.repl = parts[2]
		}
	}
	for _, k := range []string{"css", "xpath", "json", "regex"} {
		if strings.HasPrefix(s, k+":") {
			r.kind, s = k, s[len(k)+1:]
			break
		}
	}
	switch r.kind {
	case "css":
		if i := strings.LastIndex(s, "@"); i >= 0 {
			s, r.attr = s[:i], s[i+1:]
		}
	case "xpath":
		if strings.HasSuffix(s, "@@html") {
			s, r.attr = strings.TrimSuffix(s, "@@html"), "html"
		}
	}
	r.expr = strings.TrimSpace(s)
	return r
}

func (r rule) clean(values []string) []string {
	out := values[:0]
	for _, v := range values {
		if r.re != nil {
			v = r.re.ReplaceAllString(v, r.repl)
		}
		if v = strings.TrimSpace(v); v != "" {
			out = append(out, v)
		}
	}
	return out
}

func regexValues(expr, text string) []string {
	re, err := regexp.Compile(expr)
	if err != nil {
		return nil
	}
	var out []string
	for _, m := range re.FindAllStringSubmatch(text, -1) {
		out = append(out, m[len(m)-1])
	}
	return out
}

type htmlNode struct{ sel *goquery.Selection }

func (h htmlNode) Find(s string) []Node {
	r := parseRule(s, "css")
	var out []Node
	switch r.kind {
	case "css":
		h.sel.Find(r.expr).Each(func(_ int, s *goquery.Selection) { out = append(out, htmlNode{s}) })
	case "xpath":
		for _, n := range h.sel.Nodes {
			found, _ := htmlquery.QueryAll(n, r.expr)
			for _, m := range found {
				out = append(out, htmlNode{goquery.NewDocumentFromNode(m).Selection})
			}
		}
	}
	return out
}

func (h htmlNode) Values(s string) []string {
	r := parseRule(s, "css")
	var out []string
	switch r.kind {
	case "css":
		sel := h.sel
		if r.expr != "" {
			sel = sel.Find(r.expr)
		}
		sel.Each(func(_ int, s *goquery.Selection) {
			switch r.attr {
			case "", "text":
				out = append(out, s.Text())
			case "html":
				v, _ := s.Html()
				out = append(out, v)
			case "outerHtml":
				v, _ := goquery.OuterHtml(s)
				out = append(out, v)
			default:
				out = append(out, s.AttrOr(r.attr, ""))
			}
		})
	case "xpath":
		for _, n := range h.sel.Nodes {
			found, _ := htmlquery.QueryAll(n, r.expr)
			for _, m := range found {
				if r.attr == "html" {
					out = append(out, htmlquery.OutputHTML(m, false))
				} else {
					out = append(out, htmlquery.InnerText(m))
				}
			}
		}
	case "regex":
		v, _ := goquery.OuterHtml(h.sel)
		out = regexValues(r.expr, v)
	}
	return r.clean(out)
}

type jsonNode struct{ res gjson.Result }

func (j jsonNode) get(path string) gjson.Result {
	if path == "" {
		return j.res
	}
	return j.res.Get(path)
}

func (j jsonNode) Find(s string) []Node {
	r := parseRule(s, "json")
	if r.kind != "json" {
		return nil
	}
	var out []Node
	for _, item := range j.get(r.expr).Array() {
		out = append(out, jsonNode{item})
	}
	return out
}

func (j jsonNode) Values(s string) []string {
	r := parseRule(s, "json")
	var out []string
	switch r.kind {
	case "json":
		res := j.get(r.expr)
		if res.IsArray() {
			for _, item := range res.Array() {
				out = append(out, item.String())
			}
		} else if res.Exists() {
			out = append(out, res.String())
		}
	case "regex":
		out = regexValues(r.expr, j.res.Raw)
	}
	return r.clean(out)
}
