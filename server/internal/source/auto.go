package source

import (
	"cmp"
	"net/url"
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/PuerkitoBio/goquery"
	"golang.org/x/net/html"
)

// Auto as the value of a rule field leaves that field to be recognised from the page: by what
// the links of a page have in common, by the usual labels ("作者：", "下一页") and by the meta
// tags that sites publish about a book. It reads ordinary HTML pages only.
const Auto = "auto"

func isAuto(s string) bool { return strings.EqualFold(strings.TrimSpace(s), Auto) }

var (
	reDigits = regexp.MustCompile(`\d+`)
	// Chapter titles: 第12章, 12. …, Chapter 12, 番外 …
	reChapter = regexp.MustCompile(`(?i)第\s*[0-9零〇一二两三四五六七八九十百千万]+\s*[章节節回话話卷集篇部]|^\s*\d+\s*([.、:：\-—]|$)|chapter|episode|\bch\.?\s*\d|\bep\.?\s*\d|番外|序章|楔子|后记|後記|尾声|完结感言`)
	reAuthor  = regexp.MustCompile(`作\s*者\s*[:：]\s*([^\s|/，,。<>()（）]{1,24})`)
	// Links that turn a page of a list or of a chapter.
	reNextPage    = regexp.MustCompile(`(?i)^\s*(下一页|下一頁|下页|下頁|后页|next|next page|›|»|>|>>)\s*$`)
	reNextAny     = regexp.MustCompile(`(?i)下一[页頁章节節话話篇回]|下[页頁]|next`)
	reFullCatalog = regexp.MustCompile(`全部章节|完整目录|所有章节|章节目录|查看目录|全部目录|更多章节|章节列表|全部章節|章節目錄`)
	reBodyHint    = regexp.MustCompile(`(?i)content|chapter|article|read|text|txt|booktext|nr`)
	reScriptImage = regexp.MustCompile(`(?i)https?:(?:\\?/){2}[^"'\s<>\\]+(?:\\?/[^"'\s<>\\]+)*\.(?:jpe?g|png|webp)(?:\?[^"'\s<>\\]*)?`)
	reNumber      = regexp.MustCompile(`第\s*(\d+)|^\D{0,4}(\d+)`)

	chrome     = "nav, header, footer, [class*=nav], [id*=nav], [class*=menu], [class*=foot], [id*=foot], [class*=breadcrumb]"
	imageAttrs = []string{"data-original", "data-src", "data-lazy-src", "data-lazyload", "data-echo", "data-url", "src"}
)

// chromeOf finds the menus and footers of a page. An element named like one that holds most of
// the page's links is not one: a tag left open puts the whole page inside the menu.
func chromeOf(doc *goquery.Selection) map[*html.Node]bool {
	total := doc.Find("a[href]").Length()
	out := map[*html.Node]bool{}
	doc.Find(chrome).Each(func(_ int, el *goquery.Selection) {
		if el.Find("a[href]").Length()*2 <= total {
			out[el.Nodes[0]] = true
		}
	})
	return out
}

func inChrome(set map[*html.Node]bool, n *html.Node) bool {
	for p := n; p != nil; p = p.Parent {
		if set[p] {
			return true
		}
	}
	return false
}

// link is an anchor of a page that leads to another page of the same site.
type link struct {
	node  *html.Node
	href  string
	title string
	shape string
}

func squash(s string) string { return strings.Join(strings.Fields(s), " ") }

func host(u *url.URL) string {
	h := strings.ToLower(u.Hostname())
	for _, p := range []string{"www.", "m.", "wap.", "mobile."} {
		h = strings.TrimPrefix(h, p)
	}
	return h
}

// shape is what the addresses of the pages of one kind have in common: numbers become "#", and
// a last segment without any becomes "*", so /book/12/ and /book/345/ are of one shape.
func shape(u *url.URL) string {
	segs := strings.Split(strings.Trim(u.Path, "/"), "/")
	for i, s := range segs {
		gen := reDigits.ReplaceAllString(s, "#")
		if i == len(segs)-1 && gen == s && s != "" {
			gen = "*" + path.Ext(s)
		}
		segs[i] = gen
	}
	out := "/" + strings.Join(segs, "/")
	if strings.HasSuffix(u.Path, "/") && out != "/" {
		out += "/"
	}
	if q := u.Query(); len(q) > 0 {
		keys := make([]string, 0, len(q))
		for k := range q {
			keys = append(keys, k)
		}
		slices.Sort(keys)
		out += "?" + strings.Join(keys, "&")
	}
	return out
}

func linkTitle(a *goquery.Selection) string {
	// A link around a whole card names the book in a heading; its other text is author and intro.
	if inner := a.Find("h1, h2, h3, h4, h5, h6, [class*=title], [class*=name]").First(); inner.Length() > 0 {
		if t := squash(inner.Text()); t != "" {
			return t
		}
	}
	if t := squash(a.Text()); t != "" {
		return t
	}
	if t := squash(a.AttrOr("title", "")); t != "" {
		return t
	}
	return squash(a.Find("img").First().AttrOr("alt", ""))
}

// links lists the anchors of a page that lead elsewhere on the same site and have a name.
func links(doc *goquery.Selection, base string, skipChrome bool) []link {
	here, err := url.Parse(base)
	if err != nil {
		return nil
	}
	var out []link
	menus := chromeOf(doc)
	doc.Find("a[href]").Each(func(_ int, a *goquery.Selection) {
		href := resolve(base, a.AttrOr("href", ""))
		u, err := url.Parse(href)
		if href == "" || err != nil || host(u) != host(here) {
			return
		}
		u.Fragment = ""
		if u.String() == here.String() || skipChrome && inChrome(menus, a.Nodes[0]) {
			return
		}
		title := linkTitle(a)
		if title == "" || utf8.RuneCountInString(title) > 80 {
			return
		}
		out = append(out, link{a.Nodes[0], u.String(), title, shape(u)})
	})
	return out
}

func distinct(ls []link) int {
	seen := map[string]bool{}
	for _, l := range ls {
		seen[l.href] = true
	}
	return len(seen)
}

func byShape(ls []link) (map[string][]link, []string) {
	groups := map[string][]link{}
	var order []string
	for _, l := range ls {
		if _, ok := groups[l.shape]; !ok {
			order = append(order, l.shape)
		}
		groups[l.shape] = append(groups[l.shape], l)
	}
	return groups, order
}

func ancestors(n *html.Node) []*html.Node {
	var out []*html.Node
	for p := n; p != nil; p = p.Parent {
		out = append(out, p)
	}
	slices.Reverse(out)
	return out
}

// common is the innermost element that contains all of the links.
func common(ls []link) *html.Node {
	chain := ancestors(ls[0].node)
	for _, l := range ls[1:] {
		other := ancestors(l.node)
		n := 0
		for n < len(chain) && n < len(other) && chain[n] == other[n] {
			n++
		}
		chain = chain[:n]
	}
	if len(chain) == 0 {
		return nil
	}
	return chain[len(chain)-1]
}

func within(n, root *html.Node) bool {
	for p := n; p != nil; p = p.Parent {
		if p == root {
			return true
		}
	}
	return false
}

// autoBooks reads a list of books off a page. With a keyword these are the links named after
// it and the links of the same kind listed beside them; without one, the largest set of links
// of one kind, or of the kind that a search of this site returned before (known).
func autoBooks(doc *goquery.Selection, base, key, known string) (books []BookInfo, kind string) {
	all := links(doc, base, key == "")
	groups, order := byShape(all)
	var picked []link
	if key != "" {
		fold := strings.ToLower(squash(key))
		best := 0
		for _, s := range order {
			var named []link
			for _, l := range groups[s] {
				if strings.Contains(strings.ToLower(l.title), fold) {
					named = append(named, l)
				}
			}
			if n := distinct(named); n > best {
				best, kind, picked = n, s, named
			}
		}
		if best > 1 {
			// A result named differently (found by its author, say) is listed among the others.
			root := common(picked)
			picked = slices.DeleteFunc(slices.Clone(groups[kind]), func(l link) bool { return !within(l.node, root) })
		}
	} else {
		best := 0.0
		for _, s := range order {
			g := groups[s]
			n := distinct(g)
			if n < 5 || !strings.ContainsAny(s, "#*") {
				continue
			}
			chapters, pictures := 0, 0
			for _, l := range g {
				if reChapter.MatchString(l.title) {
					chapters++
				}
				if goquery.NewDocumentFromNode(l.node).Find("img").Length() > 0 {
					pictures++
				}
			}
			if chapters*3 > len(g) {
				continue
			}
			score := float64(n)
			if pictures*3 >= len(g) {
				score *= 1.5
			}
			if s == known {
				score *= 100
			}
			if score > best {
				best, kind, picked = score, s, g
			}
		}
	}
	if len(picked) == 0 {
		return nil, ""
	}

	// The item of a book is the largest element around its link that holds no other book.
	root := common(picked)
	inside := map[*html.Node]map[string]bool{}
	for _, l := range picked {
		for p := l.node; p != nil && p != root; p = p.Parent {
			if inside[p] == nil {
				inside[p] = map[string]bool{}
			}
			inside[p][l.href] = true
		}
	}
	// A book is often linked twice, from its cover and from its name: the name is the title.
	named := map[string]string{}
	for _, l := range picked {
		if t := squash(goquery.NewDocumentFromNode(l.node).Text()); t != "" && named[l.href] == "" {
			named[l.href] = l.title
		}
	}
	seen := map[string]bool{}
	for _, l := range picked {
		if seen[l.href] {
			continue
		}
		seen[l.href] = true
		l.title = cmp.Or(named[l.href], l.title)
		item := l.node
		for p := item.Parent; p != nil && p != root && len(inside[p]) == 1; p = p.Parent {
			item = p
		}
		sel := goquery.NewDocumentFromNode(item).Selection
		b := BookInfo{Title: l.title, URL: l.href, Cover: imageURL(sel.Find("img").First(), base)}
		b.Author = author(sel)
		b.Intro = labelled(sel, "[class*=intro], [class*=desc], [class*=summary], [class*=abstract]", 10)
		books = append(books, b)
		if len(books) == 100 {
			break
		}
	}
	return books, kind
}

func author(sel *goquery.Selection) string {
	if v := squash(sel.Find("[class*=author], [class*=Author], [class*=writer]").First().Text()); v != "" {
		v = regexp.MustCompile(`^作\s*者\s*[:：]?\s*`).ReplaceAllString(v, "")
		if utf8.RuneCountInString(v) <= 24 {
			return v
		}
	}
	if m := reAuthor.FindStringSubmatch(sel.Text()); m != nil {
		return m[1]
	}
	return ""
}

// labelled is the text of the first element matching the selector that says enough.
func labelled(sel *goquery.Selection, selector string, minRunes int) string {
	out := ""
	sel.Find(selector).EachWithBreak(func(_ int, s *goquery.Selection) bool {
		if v := squash(s.Text()); utf8.RuneCountInString(v) >= minRunes {
			out = v
		}
		return out == ""
	})
	return out
}

// imageURL prefers the attributes of lazily loaded pictures: their src is a placeholder.
func imageURL(img *goquery.Selection, base string) string {
	for _, attr := range imageAttrs {
		if v := strings.TrimSpace(img.AttrOr(attr, "")); v != "" && !strings.HasPrefix(v, "data:") {
			return resolve(base, v)
		}
	}
	return ""
}

func meta(doc *goquery.Selection, names ...string) string {
	for _, name := range names {
		v := ""
		doc.Find("meta").EachWithBreak(func(_ int, m *goquery.Selection) bool {
			if m.AttrOr("property", "") == name || m.AttrOr("name", "") == name {
				v = squash(m.AttrOr("content", ""))
			}
			return v == ""
		})
		if v != "" {
			return v
		}
	}
	return ""
}

// autoDetail reads one field of a book page: from the meta tags about the book where the site
// has them, else from the usual places.
func autoDetail(doc *goquery.Selection, base, field string) string {
	switch field {
	case "title":
		if v := meta(doc, "og:novel:book_name", "og:book:title"); v != "" {
			return v
		}
		// A page with several headings does not say which of them is the book.
		if h := doc.Find("h1"); h.Length() == 1 {
			h = h.Clone()
			h.Find("script, style").Remove()
			// Beside the link of a heading stand counts and labels.
			if a := h.Find("a"); a.Length() == 1 && squash(a.Text()) != "" {
				h = a
			}
			if v := squash(h.Text()); utf8.RuneCountInString(v) <= 40 {
				return v
			}
		}
	case "author":
		if v := meta(doc, "og:novel:author", "og:book:author"); v != "" {
			return v
		}
		return author(doc.Find("body"))
	case "cover":
		if v := meta(doc, "og:image"); v != "" {
			return resolve(base, v)
		}
		return imageURL(doc.Find("[class*=cover] img, [id*=cover] img, img[id*=cover], img[class*=cover], [class*=pic] img, [class*=thumb] img").First(), base)
	case "intro":
		if v := meta(doc, "og:description"); v != "" {
			return v
		}
		if v := labelled(doc, "[id*=intro], [class*=intro], [class*=desc], [class*=summary]", 2); v != "" {
			return v
		}
		return meta(doc, "description")
	case "latest":
		return meta(doc, "og:novel:latest_chapter_name", "og:novel:lastest_chapter_name")
	case "tocUrl":
		// The book page of some sites shows the newest chapters only and links to the full list.
		for _, l := range links(doc, base, false) {
			if utf8.RuneCountInString(l.title) <= 12 && reFullCatalog.MatchString(l.title) {
				return l.href
			}
		}
	}
	return ""
}

// stem is the address of a page without its extension, which the pages below it begin with.
func stem(u *url.URL) string {
	p := strings.TrimSuffix(u.Path, "/")
	p = strings.TrimSuffix(p, path.Ext(p))
	if name := path.Base(p); name == "index" || name == "list" {
		p = path.Dir(p)
	}
	return p
}

// autoChapters reads the chapter list off a catalog page: the largest set of links of one kind,
// preferring links that are titled like chapters and that lie below the book's own address.
// newest are the chapters of a block above the catalog that repeats the latest ones.
func autoChapters(doc *goquery.Selection, base string) (list, newest []ChapterInfo) {
	here, err := url.Parse(base)
	if err != nil {
		return nil, nil
	}
	groups, order := byShape(links(doc, base, true))
	var picked []link
	best := 0.0
	for _, s := range order {
		g := groups[s]
		// Links of the kind of this page lead to other books.
		if s == shape(here) {
			continue
		}
		titled, below := 0, 0
		for _, l := range g {
			if reChapter.MatchString(l.title) {
				titled++
			}
			if u, _ := url.Parse(l.href); u != nil && strings.HasPrefix(u.Path, stem(here)+"/") {
				below++
			}
		}
		n := distinct(g)
		if n < 5 && titled*2 < len(g) {
			continue
		}
		score := float64(n) * (1 + 4*float64(titled)/float64(len(g)))
		if below*2 > len(g) {
			score *= 3
		}
		if score > best {
			best, picked = score, g
		}
	}
	// The links are in runs, one for each element that lists some: the volumes of a book, or
	// the latest chapters and then all of them.
	count := map[*html.Node]int{}
	for _, l := range picked {
		for p := l.node.Parent; p != nil; p = p.Parent {
			count[p]++
		}
	}
	var runs [][]ChapterInfo
	var last *html.Node
	for _, l := range picked {
		box := l.node.Parent
		for box.Parent != nil && count[box] == 1 {
			box = box.Parent
		}
		if box != last {
			runs, last = append(runs, nil), box
		}
		runs[len(runs)-1] = append(runs[len(runs)-1], ChapterInfo{Title: l.title, URL: l.href})
	}
	for i, run := range runs {
		later := map[string]bool{}
		for _, other := range runs[i+1:] {
			for _, c := range other {
				later[c.URL] = true
			}
		}
		again := 0
		for _, c := range run {
			if later[c.URL] {
				again++
			}
		}
		// A short run above the others that runs backwards, or that the others repeat.
		if i < len(runs)-1 && len(newest) == 0 && len(run) <= 30 && (again*2 >= len(run) || len(run) > 1 && newestFirst(run)) {
			newest = run
			continue
		}
		list = append(list, run...)
	}
	return list, newest
}

// lastOf drops the chapters that are listed again further down. The newest chapters are often
// repeated above a catalog, and on every page of a paged one: where they come last is their place.
func lastOf(list []ChapterInfo) []ChapterInfo {
	var out []ChapterInfo
	seen := map[string]bool{}
	for _, c := range slices.Backward(list) {
		if !seen[c.URL] {
			seen[c.URL] = true
			out = append(out, c)
		}
	}
	slices.Reverse(out)
	return out
}

// newestFirst reports whether a catalog runs backwards, going by the numbers in the chapter
// titles, or else by the numbers in their addresses.
func newestFirst(list []ChapterInfo) bool {
	numbers := func(of func(ChapterInfo) string) []int {
		var out []int
		for _, c := range list {
			if n, err := strconv.Atoi(of(c)); err == nil {
				out = append(out, n)
			}
		}
		return out
	}
	seq := numbers(func(c ChapterInfo) string {
		m := reNumber.FindStringSubmatch(c.Title)
		if m == nil {
			return ""
		}
		return cmp.Or(m[1], m[2])
	})
	if len(seq)*2 < len(list) {
		seq = numbers(func(c ChapterInfo) string {
			u, err := url.Parse(c.URL)
			if err != nil {
				return ""
			}
			all := reDigits.FindAllString(u.Path, -1)
			if len(all) == 0 {
				return ""
			}
			return all[len(all)-1]
		})
	}
	up, down := 0, 0
	for i := 1; i < len(seq); i++ {
		switch {
		case seq[i] > seq[i-1]:
			up++
		case seq[i] < seq[i-1]:
			down++
		}
	}
	return down > up
}

// autoNextList is the link to the next page of a paged catalog.
func autoNextList(doc *goquery.Selection, base string) string {
	for _, l := range links(doc, base, false) {
		if reNextPage.MatchString(l.title) {
			return l.href
		}
	}
	return ""
}

// autoNextPart is the link to the rest of a chapter that is split over pages. Such a page is
// addressed like the first one with a suffix (12.html, 12_2.html); the same link leads to the
// next chapter from the last page, which is not followed.
func autoNextPart(doc *goquery.Selection, base, first string) string {
	start, err := url.Parse(first)
	if err != nil {
		return ""
	}
	for _, l := range links(doc, base, false) {
		u, err := url.Parse(l.href)
		if err != nil || !reNextAny.MatchString(l.title) {
			continue
		}
		rest, ok := strings.CutPrefix(u.Path, stem(start))
		if ok && rest != "" && strings.ContainsRune("_-/", rune(rest[0])) || u.Path == start.Path && u.RawQuery != start.RawQuery {
			return l.href
		}
	}
	return ""
}

// autoBody finds the text of a chapter: the element with the most text of its own, as opposed
// to text in elements nested further down, which is what the page around a chapter consists of.
func autoBody(doc *goquery.Selection) string {
	var body *goquery.Selection
	best, menus := 0.0, chromeOf(doc)
	doc.Find("div, article, section, main, td, dd, body").Each(func(_ int, el *goquery.Selection) {
		if inChrome(menus, el.Nodes[0]) {
			return
		}
		own := 0
		for c := el.Nodes[0].FirstChild; c != nil; c = c.NextSibling {
			switch {
			case c.Type == html.TextNode:
				own += utf8.RuneCountInString(strings.TrimSpace(c.Data))
			case c.Type == html.ElementNode && c.Data == "p":
				own += utf8.RuneCountInString(strings.TrimSpace(goquery.NewDocumentFromNode(c).Text()))
			}
		}
		score := float64(own)
		if reBodyHint.MatchString(el.AttrOr("id", "") + " " + el.AttrOr("class", "")) {
			score *= 1.3
		}
		if score > best {
			best, body = score, el
		}
	})
	if body == nil || best < 20 {
		return ""
	}
	// Links inside the text are navigation and notices, not prose.
	body = body.Clone()
	if goquery.NodeName(body) == "body" {
		// Text that lies loose in the page has the rest of the page for siblings.
		body.Children().Not("p, br").Remove()
	}
	body.Find("script, style, a, ins, iframe, button, form, select").Remove()
	out, _ := body.Html()
	return out
}

// autoImages finds the pages of a comic chapter: the largest run of pictures that sit together,
// or else the pictures that a script of the page lists, or else the one picture that a chapter
// drawn as a single strip consists of.
func autoImages(doc *goquery.Selection, base string) []string {
	imgs := doc.Find("img")
	count := map[*html.Node]int{}
	imgs.Each(func(_ int, img *goquery.Selection) {
		for p := img.Nodes[0].Parent; p != nil; p = p.Parent {
			count[p]++
		}
	})
	type run struct {
		urls   []string
		linked int
	}
	runs := map[*html.Node]*run{}
	var order []*html.Node
	menus := chromeOf(doc)
	imgs.Each(func(_ int, img *goquery.Selection) {
		u := imageURL(img, base)
		if u == "" || inChrome(menus, img.Nodes[0]) {
			return
		}
		// Past the wrappers of this one picture is the element that holds the run.
		box := img.Nodes[0].Parent
		for box.Parent != nil && count[box] == 1 {
			box = box.Parent
		}
		if runs[box] == nil {
			runs[box] = &run{}
			order = append(order, box)
		}
		r := runs[box]
		if !slices.Contains(r.urls, u) {
			r.urls = append(r.urls, u)
		}
		if img.Closest("a[href]").Length() > 0 {
			r.linked++
		}
	})
	// Pictures that are links are mostly the covers of other books: they are the pages only if
	// nothing else is, neither pictures of their own nor a list in a script.
	var plain, linked, listed, single []string
	alone := 0
	for _, box := range order {
		r := runs[box]
		into := &plain
		if r.linked*2 > len(r.urls) {
			into = &linked
		}
		if len(r.urls) >= 2 && len(r.urls) > len(*into) {
			*into = r.urls
		}
		// A picture on its own is the chapter only if it is the only one that leads nowhere:
		// among several there is no telling the strip from a banner.
		if len(r.urls) == 1 && r.linked == 0 {
			alone++
			single = r.urls
		}
	}
	if alone != 1 {
		single = nil
	}
	doc.Find("script").Each(func(_ int, s *goquery.Selection) {
		var urls []string
		for _, u := range reScriptImage.FindAllString(s.Text(), -1) {
			if u = strings.ReplaceAll(u, `\/`, "/"); !slices.Contains(urls, u) {
				urls = append(urls, u)
			}
		}
		if len(urls) >= 2 && len(urls) > len(listed) {
			listed = urls
		}
	})
	for _, found := range [][]string{plain, listed, single, linked} {
		if found != nil {
			return found
		}
	}
	return nil
}

// endpoint is where a site takes a search, with {{key}} for the keyword.
type endpoint struct {
	method, url, body string
	// charset is what the site's pages are written in when that is not UTF-8.
	charset string
}

var (
	searchNames  = []string{"q", "s", "wd", "kw", "key", "keyword", "keywords", "searchkey", "search", "query", "word", "name", "k"}
	reCharset    = regexp.MustCompile(`(?i)charset=["']?([\w-]+)`)
	reJSONSearch = regexp.MustCompile(`"(?:target|urlTemplate)"\s*:\s*"([^"]*\{search_term_string\}[^"]*)"`)
)

// autoSearch finds the search of a site on its home page: the form with a text field, or the
// address that the page publishes for search engines.
func autoSearch(doc *goquery.Selection, base string) (endpoint, bool) {
	ep := endpoint{}
	if cs := doc.Find("meta[charset]").AttrOr("charset", ""); cs != "" {
		ep.charset = cs
	} else if m := reCharset.FindStringSubmatch(doc.Find("meta[http-equiv]").AttrOr("content", "")); m != nil {
		ep.charset = m[1]
	}
	if strings.EqualFold(ep.charset, "utf-8") || strings.EqualFold(ep.charset, "utf8") {
		ep.charset = ""
	}

	var form *goquery.Selection
	field, best := "", -1
	doc.Find("form").Each(func(_ int, f *goquery.Selection) {
		if f.Find("input[type=password]").Length() > 0 {
			return
		}
		about := strings.ToLower(f.AttrOr("action", "") + " " + f.AttrOr("id", "") + " " + f.AttrOr("class", "") + " " + f.AttrOr("role", ""))
		f.Find("input[name]").Each(func(_ int, in *goquery.Selection) {
			kind := strings.ToLower(in.AttrOr("type", "text"))
			if kind != "text" && kind != "search" {
				return
			}
			score := 0
			if kind == "search" {
				score += 2
			}
			if slices.Contains(searchNames, strings.ToLower(in.AttrOr("name", ""))) {
				score += 2
			}
			if strings.Contains(about, "search") || strings.Contains(about, "sou") {
				score += 2
			}
			if score > best {
				best, form, field = score, f, in.AttrOr("name", "")
			}
		})
	})
	if form != nil {
		var params []string
		form.Find("input[type=hidden][name]").Each(func(_ int, in *goquery.Selection) {
			params = append(params, url.QueryEscape(in.AttrOr("name", ""))+"="+url.QueryEscape(in.AttrOr("value", "")))
		})
		form.Find("select[name]").Each(func(_ int, s *goquery.Selection) {
			opt := s.Find("option[selected]").First()
			if opt.Length() == 0 {
				opt = s.Find("option").First()
			}
			params = append(params, url.QueryEscape(s.AttrOr("name", ""))+"="+url.QueryEscape(opt.AttrOr("value", "")))
		})
		query := strings.Join(append(params, url.QueryEscape(field)+"={{key}}"), "&")
		ep.url = base
		if action := resolve(base, form.AttrOr("action", "")); action != "" {
			ep.url = action
		}
		if strings.EqualFold(form.AttrOr("method", ""), "post") {
			ep.method, ep.body = "POST", query
		} else if strings.Contains(ep.url, "?") {
			ep.url += "&" + query
		} else {
			ep.url += "?" + query
		}
		return ep, true
	}
	if m := reJSONSearch.FindStringSubmatch(doc.Find(`script[type="application/ld+json"]`).Text()); m != nil {
		ep.url = resolve(base, strings.ReplaceAll(strings.ReplaceAll(m[1], `\/`, "/"), "{search_term_string}", "{{key}}"))
		// resolve escapes the braces of the placeholder.
		ep.url = strings.NewReplacer("%7B%7Bkey%7D%7D", "{{key}}").Replace(ep.url)
		return ep, true
	}
	return ep, false
}

// searchPage is the page a site keeps its search form on when the home page only links to it.
func searchPage(doc *goquery.Selection, base string) string {
	here, err := url.Parse(base)
	if err != nil {
		return ""
	}
	found := ""
	doc.Find("a[href]").EachWithBreak(func(_ int, a *goquery.Selection) bool {
		href := resolve(base, a.AttrOr("href", ""))
		u, err := url.Parse(href)
		if href == "" || err != nil || host(u) != host(here) || u.Path == here.Path {
			return true
		}
		text := squash(a.Text())
		if strings.Contains(strings.ToLower(u.Path), "search") || utf8.RuneCountInString(text) <= 6 && strings.Contains(text, "搜索") {
			found = href
		}
		return found == ""
	})
	return found
}
