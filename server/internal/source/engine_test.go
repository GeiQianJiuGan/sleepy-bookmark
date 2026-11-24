package source_test

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/GeiQianJiuGan/sleepy-bookmark/server/internal/demo"
	"github.com/GeiQianJiuGan/sleepy-bookmark/server/internal/source"
)

func setup(t *testing.T) (*source.Engine, []source.Source) {
	t.Helper()
	srv := httptest.NewServer(demo.Handler())
	t.Cleanup(srv.Close)
	sources := demo.Sources(srv.URL)
	for i := range sources {
		sources[i].RateLimitMs = 1
		if err := sources[i].Validate(); err != nil {
			t.Fatal(err)
		}
	}
	return &source.Engine{Fetcher: source.NewFetcher()}, sources
}

func TestNovelSource(t *testing.T) {
	e, sources := setup(t)
	src, ctx := &sources[0], context.Background()

	books, err := e.Search(ctx, src, "星河", 1)
	if err != nil || len(books) != 1 {
		t.Fatalf("search: %v, %d books", err, len(books))
	}
	b := books[0]
	if b.Title != "演示小说：星河旅人" || b.Author != "演示作者甲" || !strings.HasSuffix(b.URL, "/demo/novel/book/1/") ||
		!strings.HasSuffix(b.Cover, "/demo/cover/1.png") {
		t.Fatalf("unexpected search hit: %+v", b)
	}

	detail, err := e.Detail(ctx, src, b.URL)
	if err != nil || detail.Author != "演示作者甲" || detail.TocURL != b.URL {
		t.Fatalf("detail: %v %+v", err, detail)
	}

	toc, err := e.Toc(ctx, src, detail.TocURL)
	if err != nil || len(toc) != 30 {
		t.Fatalf("toc: %v, %d chapters", err, len(toc))
	}
	if toc[0].Title != "第1章 演示章节" || !strings.HasSuffix(toc[0].URL, "/book/1/1.html") {
		t.Fatalf("unexpected first chapter: %+v", toc[0])
	}

	c, err := e.Content(ctx, src, toc[0].URL, toc[1].URL)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(c.Text, "\n")
	// Two pages of three kept paragraphs each; the ad line is purified and chapter 2 is not followed.
	if len(lines) != 6 || lines[0] != "这是《演示小说：星河旅人》第1章第1页的第一段。" || lines[5] != "第1章第2页的最后一段。" {
		t.Fatalf("unexpected content:\n%s", c.Text)
	}
	if strings.Contains(c.Text, "本站域名") {
		t.Fatal("ad line was not purified")
	}
}

func TestComicSource(t *testing.T) {
	e, sources := setup(t)
	src, ctx := &sources[1], context.Background()

	books, err := e.Search(ctx, src, "", 1)
	if err != nil || len(books) != 2 {
		t.Fatalf("search: %v, %d books", err, len(books))
	}
	toc, err := e.Toc(ctx, src, books[0].URL)
	if err != nil || len(toc) != 8 {
		t.Fatalf("toc: %v, %d chapters", err, len(toc))
	}
	if toc[0].Title != "第1话" {
		t.Fatalf("toc was not reversed: %+v", toc[0])
	}
	c, err := e.Content(ctx, src, toc[0].URL, toc[1].URL)
	if err != nil || len(c.Images) != 6 || !strings.HasSuffix(c.Images[0], "/demo/comic/img/1/1/1.png") {
		t.Fatalf("content: %v %+v", err, c)
	}
	img, err := e.Fetcher.Image(ctx, src, c.Images[0])
	if err != nil || !strings.HasPrefix(string(img), "\x89PNG") {
		t.Fatalf("image should be fetched with the source Referer: %v", err)
	}
}
