// Package demo is a small fake novel site (HTML) and comic site (JSON API) served by the backend
// itself, so the whole stack can be tried and tested without depending on a real source.
package demo

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html/template"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"golang.org/x/image/font"
	"golang.org/x/image/font/basicfont"
	"golang.org/x/image/math/fixed"

	"github.com/GeiQianJiuGan/sleepy-bookmark/server/internal/source"
)

type book struct {
	ID       int
	Title    string
	Author   string
	Intro    string
	Chapters int
}

var novels = []book{
	{1, "演示小说：星河旅人", "演示作者甲", "一位旅人穿行在群星之间的故事。仅用于演示书源规则。", 30},
	{2, "演示小说：山海札记", "演示作者乙", "记录山与海之间的见闻。仅用于演示书源规则。", 12},
	{3, "长夜灯火", "演示作者丙", "守夜人和一盏不灭的灯。", 6},
	{4, "北境来信", "演示作者丁", "十二封从北方寄来的信。", 6},
	{5, "机关城", "演示作者甲", "一座会移动的城。", 6},
	{6, "潮汐之书", "演示作者乙", "潮起潮落之间的一年。", 6},
	{7, "纸上江湖", "演示作者丙", "说书人笔下的江湖。", 6},
	{8, "云端邮局", "演示作者丁", "把信送到云上去。", 6},
	{9, "第七个站台", "演示作者甲", "一趟没有终点的列车。", 6},
	{10, "旧城地图", "演示作者乙", "按一张旧地图寻路。", 6},
	{11, "雪线以上", "演示作者丙", "登山队的日志。", 6},
	{12, "长夜灯火", "演示作者戊", "同名的另一本书。", 6},
}

var comics = []book{
	{1, "演示漫画：色块物语", "演示画师甲", "每一页都是一张自动生成的色块图。", 8},
	{2, "演示漫画：数字迷宫", "演示画师乙", "用页码拼成的迷宫。", 5},
	{3, "方格猫", "演示画师丙", "住在格子里的猫。", 3},
	{4, "折纸飞船", "演示画师甲", "一张纸折出的远航。", 3},
}

const (
	pagesPerChapter  = 2
	imagesPerChapter = 6
)

func find(list []book, id string) *book {
	n, _ := strconv.Atoi(id)
	for i := range list {
		if list[i].ID == n {
			return &list[i]
		}
	}
	return nil
}

func ranked(list []book) []book {
	out := slices.Clone(list)
	slices.SortStableFunc(out, func(a, b book) int { return b.Chapters - a.Chapters })
	return out
}

func match(list []book, key string) []book {
	var out []book
	for _, b := range list {
		if key == "" || strings.Contains(b.Title, key) || strings.Contains(b.Author, key) {
			out = append(out, b)
		}
	}
	return out
}

var tpl = template.Must(template.New("").Funcs(template.FuncMap{
	"seq": func(n int) []int {
		out := make([]int, n)
		for i := range out {
			out[i] = i + 1
		}
		return out
	},
}).Parse(`
{{define "home"}}<html><body>
<form action="search" method="get"><input type="text" name="q"><button type="submit">搜索</button></form>
{{template "list" .}}</body></html>{{end}}

{{define "search"}}<html><body>{{template "list" .}}</body></html>{{end}}

{{define "list"}}<ul class="result">
{{range .}}<li>
  <img src="/demo/cover/{{.ID}}.png">
  <a class="name" href="book/{{.ID}}/">{{.Title}}</a>
  <span class="author">{{.Author}}</span>
  <p class="intro">{{.Intro}}</p>
</li>{{end}}
</ul>{{end}}

{{define "book"}}<html><body>
<div id="info"><h1>{{.Title}}</h1><p class="author">作者：{{.Author}}</p>
<img id="cover" src="/demo/cover/{{.ID}}.png"><div id="intro">{{.Intro}}</div></div>
<div id="list"><dl>
{{$id := .ID}}{{range seq .Chapters}}<dd><a href="{{.}}.html">第{{.}}章 演示章节</a></dd>
{{end}}</dl></div></body></html>{{end}}

{{define "chapter"}}<html><body><h1>第{{.N}}章 演示章节</h1>
<div id="content">
&nbsp;&nbsp;&nbsp;&nbsp;这是《{{.Book.Title}}》第{{.N}}章第{{.Page}}页的第一段。<br><br>
&nbsp;&nbsp;&nbsp;&nbsp;正文段落用来验证分页抓取、换行整理和广告净化。<br><br>
&nbsp;&nbsp;&nbsp;&nbsp;请记住本站域名 demo.example，最新章节抢先看！<br><br>
&nbsp;&nbsp;&nbsp;&nbsp;第{{.N}}章第{{.Page}}页的最后一段。
</div>
<div class="nav">{{if .Next}}<a id="next" href="{{.Next}}">{{.NextLabel}}</a>{{end}}</div>
</body></html>{{end}}
`))

// Handler serves the demo sites; mount it under /demo.
func Handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /demo/novel/search", func(w http.ResponseWriter, r *http.Request) {
		tpl.ExecuteTemplate(w, "search", match(novels, r.URL.Query().Get("q")))
	})
	// The home page is what a source of nothing but this address is recognised from.
	mux.HandleFunc("GET /demo/novel/{$}", func(w http.ResponseWriter, r *http.Request) {
		tpl.ExecuteTemplate(w, "home", ranked(novels))
	})
	// The ranking lists every book, most chapters first.
	mux.HandleFunc("GET /demo/novel/rank", func(w http.ResponseWriter, r *http.Request) {
		tpl.ExecuteTemplate(w, "search", ranked(novels))
	})
	mux.HandleFunc("GET /demo/novel/book/{id}/{$}", func(w http.ResponseWriter, r *http.Request) {
		b := find(novels, r.PathValue("id"))
		if b == nil {
			http.NotFound(w, r)
			return
		}
		tpl.ExecuteTemplate(w, "book", b)
	})
	// Chapter N is split over N.html and N_2.html to exercise content.nextPage.
	mux.HandleFunc("GET /demo/novel/book/{id}/{file}", func(w http.ResponseWriter, r *http.Request) {
		b := find(novels, r.PathValue("id"))
		name, ok := strings.CutSuffix(r.PathValue("file"), ".html")
		if b == nil || !ok {
			http.NotFound(w, r)
			return
		}
		ns, ps, _ := strings.Cut(name, "_")
		n, _ := strconv.Atoi(ns)
		p := 1
		if ps != "" {
			p, _ = strconv.Atoi(ps)
		}
		if n < 1 || n > b.Chapters || p < 1 || p > pagesPerChapter {
			http.NotFound(w, r)
			return
		}
		data := map[string]any{"Book": b, "N": n, "Page": p}
		switch {
		case p < pagesPerChapter:
			data["Next"], data["NextLabel"] = fmt.Sprintf("%d_%d.html", n, p+1), "下一页"
		case n < b.Chapters:
			data["Next"], data["NextLabel"] = fmt.Sprintf("%d.html", n+1), "下一章"
		}
		tpl.ExecuteTemplate(w, "chapter", data)
	})

	writeJSON := func(w http.ResponseWriter, v any) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": v})
	}
	comicJSON := func(b book) map[string]any {
		return map[string]any{"id": b.ID, "name": b.Title, "author": b.Author, "desc": b.Intro,
			"cover": fmt.Sprintf("/demo/cover/%d.png", b.ID+10)}
	}
	mux.HandleFunc("GET /demo/comic/api/search", func(w http.ResponseWriter, r *http.Request) {
		list := []map[string]any{}
		for _, b := range match(comics, r.URL.Query().Get("q")) {
			list = append(list, comicJSON(b))
		}
		writeJSON(w, map[string]any{"list": list})
	})
	mux.HandleFunc("GET /demo/comic/api/rank", func(w http.ResponseWriter, r *http.Request) {
		list := []map[string]any{}
		for _, b := range ranked(comics) {
			list = append(list, comicJSON(b))
		}
		writeJSON(w, map[string]any{"list": list})
	})
	mux.HandleFunc("GET /demo/comic/api/book/{id}", func(w http.ResponseWriter, r *http.Request) {
		b := find(comics, r.PathValue("id"))
		if b == nil {
			http.NotFound(w, r)
			return
		}
		out := comicJSON(*b)
		chapters := []map[string]any{}
		// Newest first, like many comic sites; the source rule sets toc.reverse.
		for n := b.Chapters; n >= 1; n-- {
			chapters = append(chapters, map[string]any{"name": fmt.Sprintf("第%d话", n), "path": fmt.Sprintf("%d/chapter/%d", b.ID, n)})
		}
		out["chapters"] = chapters
		writeJSON(w, out)
	})
	mux.HandleFunc("GET /demo/comic/api/book/{id}/chapter/{n}", func(w http.ResponseWriter, r *http.Request) {
		b := find(comics, r.PathValue("id"))
		n, _ := strconv.Atoi(r.PathValue("n"))
		if b == nil || n < 1 || n > b.Chapters {
			http.NotFound(w, r)
			return
		}
		images := []string{}
		for p := 1; p <= imagesPerChapter; p++ {
			images = append(images, fmt.Sprintf("/demo/comic/img/%d/%d/%d.png", b.ID, n, p))
		}
		writeJSON(w, map[string]any{"images": images})
	})
	// Comic pages reject requests without the site's Referer, like real anti-hotlinking.
	mux.HandleFunc("GET /demo/comic/img/{id}/{n}/{file}", func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.Referer(), "/demo/comic") {
			http.Error(w, "hotlinking forbidden", http.StatusForbidden)
			return
		}
		id, _ := strconv.Atoi(r.PathValue("id"))
		n, _ := strconv.Atoi(r.PathValue("n"))
		p, _ := strconv.Atoi(strings.TrimSuffix(r.PathValue("file"), ".png"))
		writePNG(w, picture(60, 90, id*100+n*7+p, fmt.Sprintf("#%d", n), fmt.Sprintf("P%d", p)))
	})
	mux.HandleFunc("GET /demo/cover/{file}", func(w http.ResponseWriter, r *http.Request) {
		id, _ := strconv.Atoi(strings.TrimSuffix(r.PathValue("file"), ".png"))
		writePNG(w, picture(30, 40, id*37, "DEMO", strconv.Itoa(id)))
	})
	return mux
}

// picture draws a tiny labelled colour card and scales it up 10x.
func picture(w, h, seed int, lines ...string) image.Image {
	small := image.NewRGBA(image.Rect(0, 0, w, h))
	bg := color.RGBA{uint8(80 + seed*53%150), uint8(80 + seed*97%150), uint8(80 + seed*31%150), 255}
	draw.Draw(small, small.Bounds(), image.NewUniform(bg), image.Point{}, draw.Src)
	d := font.Drawer{Dst: small, Src: image.White, Face: basicfont.Face7x13}
	for i, line := range lines {
		d.Dot = fixed.P((w-len(line)*7)/2, h/2+i*14-2)
		d.DrawString(line)
	}
	const scale = 10
	big := image.NewRGBA(image.Rect(0, 0, w*scale, h*scale))
	for y := 0; y < h*scale; y++ {
		for x := 0; x < w*scale; x++ {
			big.SetRGBA(x, y, small.RGBAAt(x/scale, y/scale))
		}
	}
	return big
}

func writePNG(w http.ResponseWriter, img image.Image) {
	var buf bytes.Buffer
	png.Encode(&buf, img)
	w.Header().Set("Content-Type", "image/png")
	w.Write(buf.Bytes())
}

// Sources returns the rules for the demo sites served at base (e.g. http://127.0.0.1:8080).
func Sources(base string) []source.Source {
	return []source.Source{
		{
			Name:    "演示小说源",
			Type:    source.TypeNovel,
			BaseURL: base + "/demo/novel",
			Search: source.SearchRule{
				URL:     "/search?q={{key}}&page={{page}}",
				List:    "css:.result li",
				Title:   "css:a.name",
				URLRule: "css:a.name@href",
				Author:  "css:.author",
				Cover:   "css:img@src",
				Intro:   "css:.intro",
			},
			Recommend: &source.ListRule{
				URL:     "/rank",
				List:    "css:.result li",
				Title:   "css:a.name",
				URLRule: "css:a.name@href",
				Author:  "css:.author",
				Cover:   "css:img@src",
				Intro:   "css:.intro",
			},
			Detail: source.DetailRule{
				Title:  "css:#info h1",
				Author: "css:#info .author##^作者：",
				Cover:  "css:#cover@src",
				Intro:  "css:#intro",
			},
			Toc: source.TocRule{List: "css:#list dd a", Name: "@text", URL: "@href"},
			Content: source.ContentRule{
				Body:     "css:#content",
				NextPage: "xpath://a[@id='next']/@href",
				Purify:   []string{`请记住本站域名.*`},
			},
		},
		{
			Name:    "演示漫画源",
			Type:    source.TypeComic,
			BaseURL: base + "/demo/comic",
			Search: source.SearchRule{
				URL:     "/api/search?q={{key}}",
				List:    "json:data.list",
				Title:   "name",
				URLRule: "id##^##book/",
				Author:  "author",
				Cover:   "cover",
				Intro:   "desc",
			},
			Recommend: &source.ListRule{
				URL: "/api/rank", List: "json:data.list", Title: "name", URLRule: "id##^##book/",
				Author: "author", Cover: "cover", Intro: "desc",
			},
			Detail:  source.DetailRule{Title: "data.name", Author: "data.author", Cover: "data.cover", Intro: "data.desc"},
			Toc:     source.TocRule{List: "data.chapters", Name: "name", URL: "path", Reverse: true},
			Content: source.ContentRule{Images: "data.images"},
		},
	}
}
