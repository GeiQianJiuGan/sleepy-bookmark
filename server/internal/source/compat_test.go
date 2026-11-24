package source

import "testing"

func TestParseSourceFlexibleLegado(t *testing.T) {
	legadoComic := `{
		"bookSourceName": "示例漫画源",
		"bookSourceUrl": "https://comic.example.com",
		"bookSourceType": 2,
		"header": "{\"Referer\":\"https://comic.example.com/\"}",
		"searchUrl": "/search?q={{key}}&p={{page}}",
		"ruleSearch": {
			"bookList": "class.item",
			"name": "tag.a.0@text",
			"bookUrl": "tag.a.0@href",
			"coverUrl": "tag.img@data-src",
			"author": "class.author@text##^作者："
		},
		"ruleToc": {
			"chapterList": "-id.chapters@tag.a",
			"chapterName": "@text",
			"chapterUrl": "@href"
		},
		"ruleContent": {
			"content": "class.comic-page@tag.img@data-src"
		}
	}`
	src, err := ParseSourceFlexible([]byte(legadoComic))
	if err != nil {
		t.Fatalf("expected conversion success, got: %v", err)
	}
	if src.Name != "示例漫画源" || src.Type != TypeComic || src.BaseURL != "https://comic.example.com" {
		t.Fatalf("unexpected header fields: %+v", src)
	}
	if src.Search.List != "css:.item" || src.Search.Title != "css:a@text" || src.Search.URLRule != "css:a@href" {
		t.Fatalf("unexpected search rules: %+v", src.Search)
	}
	if src.Search.Author != "css:.author@text##^作者：" {
		t.Fatalf("unexpected author rule: %q", src.Search.Author)
	}
	if !src.Toc.Reverse || src.Toc.List != "css:#chapters a" {
		t.Fatalf("unexpected toc rule: %+v", src.Toc)
	}
	if src.Content.Images != "css:.comic-page img@data-src" {
		t.Fatalf("unexpected images rule: %q", src.Content.Images)
	}
	if src.Headers["Referer"] != "https://comic.example.com/" {
		t.Fatalf("unexpected headers: %+v", src.Headers)
	}

	// Legado rule containing JavaScript must be rejected clearly.
	legadoJS := `{
		"bookSourceName": "JS源",
		"bookSourceUrl": "https://js.example.com",
		"bookSourceType": 0,
		"searchUrl": "/s?q={{key}}",
		"ruleSearch": {
			"bookList": "@js:result.items",
			"name": "title",
			"bookUrl": "url"
		}
	}`
	if _, err := ParseSourceFlexible([]byte(legadoJS)); err == nil {
		t.Fatal("expected error for JS Legado rule")
	}

	// Incomplete foreign object (no rule blocks) must not match Legado conversion.
	foreign := `{"bookSourceName": "别的格式", "bookSourceUrl": "https://example.com"}`
	if _, err := ParseSourceFlexible([]byte(foreign)); err == nil {
		t.Fatal("expected error for incomplete foreign object")
	}

	// Legado 2.0 / Yiciyuan flat-field comic source.
	flatComic := `{
		"bookSourceName": "扁平漫画源",
		"bookSourceType": "漫画",
		"bookSourceUrl": "https://flat.example.com",
		"ruleSearchUrl": "/search?q=searchKey&p=searchPage",
		"ruleSearchList": "class.card",
		"ruleSearchName": "tag.a@text",
		"ruleSearchNoteUrl": "tag.a@href",
		"ruleChapterList": "-class.chapters@tag.a",
		"ruleChapterName": "@text",
		"ruleContentUrl": "@href",
		"ruleBookContent": "img@data-original"
	}`
	fc, err := ParseSourceFlexible([]byte(flatComic))
	if err != nil {
		t.Fatalf("expected flat comic conversion success, got: %v", err)
	}
	if fc.Type != TypeComic || fc.Search.URL != "/search?q={{key}}&p={{page}}" || fc.Content.Images != "css:img@data-original" {
		t.Fatalf("unexpected flat comic conversion: %+v", fc)
	}
}

func TestALegadoComicListIsReadWithoutItsScripts(t *testing.T) {
	flat := `{
		"bookSourceName": "示例图源",
		"bookSourceUrl": "https://comic.example.com",
		"bookSourceType": "漫画",
		"ruleSearchUrl": "https://comic.example.com/s?q=searchKey&p=searchPage",
		"ruleSearchList": ".results|.items",
		"ruleSearchName": "h3@text\n@js:\njava.put(\"name\", result)",
		"ruleSearchNoteUrl": "h3 a@href",
		"ruleChapterList": ".box@.row!0@.chapters||.row@.chapters",
		"ruleChapterName": "a@text\n@js:\na = \" \" + result",
		"ruleContentUrl": "a@href",
		"ruleBookContent": "class.pages@html"
	}`
	src, err := ParseSourceFlexible([]byte(flat))
	if err != nil {
		t.Fatal(err)
	}
	for got, want := range map[string]string{
		src.Search.List:    "css:.results, .items",
		src.Search.Title:   "css:h3@text",
		src.Toc.List:       "css:.box .row .chapters, .row .chapters",
		src.Toc.Name:       "css:a@text",
		src.Content.Images: "css:.pages img@src",
	} {
		if got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	}

	// A script where the address of a chapter comes from cannot be left out.
	scripted := `{
		"bookSourceName": "示例图源",
		"bookSourceUrl": "https://comic.example.com",
		"bookSourceType": "漫画",
		"ruleSearchUrl": "https://comic.example.com/s?q=searchKey",
		"ruleSearchList": ".results",
		"ruleSearchName": "h3@text",
		"ruleSearchNoteUrl": "h3 a@href",
		"ruleChapterList": ".chapters",
		"ruleChapterName": "a@text",
		"ruleContentUrl": "a@href@js:result + '?x=1'",
		"ruleBookContent": "img@src"
	}`
	if _, err := ParseSourceFlexible([]byte(scripted)); err == nil {
		t.Error("a rule whose chapter address is a script was imported")
	}

	// Older lists keep comics as text sources and name them by their group.
	nested := `{
		"bookSourceName": "示例",
		"bookSourceGroup": "🎨漫画",
		"bookSourceUrl": "https://comic.example.com",
		"bookSourceType": 0,
		"searchUrl": "/search?q={{key}}",
		"ruleSearch": {"bookList": "class.item", "name": "tag.a.0@text", "bookUrl": "tag.a.0@href"},
		"ruleToc": {"chapterList": "id.chapters@tag.a", "chapterName": "text", "chapterUrl": "href"},
		"ruleContent": {"content": "class.man_img@html", "imageStyle": "FULL"}
	}`
	src, err = ParseSourceFlexible([]byte(nested))
	if err != nil {
		t.Fatal(err)
	}
	if src.Type != TypeComic || src.Content.Images != "css:.man_img img@src" {
		t.Errorf("type %q, images %q", src.Type, src.Content.Images)
	}
}

func TestLegadoImagesAndBars(t *testing.T) {
	for in, want := range map[string]string{
		"css:img.page":         "css:img.page@src",
		"css:.pages img":       "css:.pages img@src",
		"css:.man_img@html":    "css:.man_img img@src",
		"css:.comic-img":       "css:.comic-img img@src",
		"css:div>img[data-x]":  "css:div>img[data-x]@src",
		"css:amp-img@data-src": "css:amp-img@data-src",
	} {
		if got := legadoImages(in); got != want {
			t.Errorf("%s: %q, want %q", in, got, want)
		}
	}
	for in, want := range map[string]string{
		"li[lang|=zh]":          "css:li[lang|=zh]",
		"./li|./dd":             "xpath:./li|./dd",
		".a|.b":                 "css:.a, .b",
		".d a[href]|.m a[href]": "css:.d a[href], .m a[href]",
	} {
		if got := convertLegadoExpr(in, true); got != want {
			t.Errorf("%s: %q, want %q", in, got, want)
		}
	}
}
