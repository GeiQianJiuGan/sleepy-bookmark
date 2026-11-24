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
}
