package library

import (
	"slices"
	"testing"

	"github.com/GeiQianJiuGan/sleepy-bookmark/server/internal/store"
)

func feedList(sourceID int64, books ...store.Book) Recommendation {
	out := Recommendation{SourceID: sourceID}
	for i := range books {
		books[i].SourceID = sourceID
		if books[i].ID == 0 {
			books[i].ID = sourceID*1000 + int64(i)
		}
		out.Books = append(out.Books, &books[i])
	}
	return out
}

func feedTitles(feed []FeedBook) (out []string) {
	for _, b := range feed {
		out = append(out, b.Title)
	}
	return out
}

func TestFeedMergesTheListsOfAllSources(t *testing.T) {
	lists := []Recommendation{
		feedList(1,
			store.Book{Title: "独家", Author: "甲"},
			store.Book{Title: "《共有》", Author: "乙"},
			store.Book{Title: "同名", Author: "丙"},
			store.Book{Title: "在架"}),
		feedList(2,
			store.Book{Title: "共有", Author: "乙", Cover: "c.jpg"},
			store.Book{Title: "同名", Author: "丁"},
			store.Book{Title: "共 有"},
			store.Book{Title: "新作", Author: "老作者"}),
	}
	known := []*store.ShelfBook{{Book: store.Book{ID: 1, Title: "在架"}}, {Book: store.Book{ID: 2, Title: "旧作", Author: "老作者"}}}
	feed := buildFeed(lists, known, "day")

	titles := feedTitles(feed)
	slices.Sort(titles)
	// 共有 is one book, 同名 by two authors is two, the book on the shelf is left out.
	if want := []string{"共有", "同名", "同名", "新作", "独家"}; !slices.Equal(titles, want) {
		t.Fatalf("feed %q, want %q", titles, want)
	}
	// Two sources list it, so it beats the first place of one list; the copy with the cover
	// stands for it.
	if b := feed[0]; b.Title != "共有" || b.Sources != 2 || b.SourceID != 2 || b.Reason != "2 个书源上榜" {
		t.Errorf("first: %q from source %d, %d sources, reason %q", b.Title, b.SourceID, b.Sources, b.Reason)
	}
	for _, b := range feed {
		if b.Title == "新作" && b.Reason != "读过这位作者" {
			t.Errorf("book by a known author: reason %q", b.Reason)
		}
	}
	if again := feedTitles(buildFeed(lists, known, "day")); !slices.Equal(again, feedTitles(feed)) {
		t.Errorf("the same seed gave another order: %q, %q", feedTitles(feed), again)
	}
}

func TestFeedAlternatesSources(t *testing.T) {
	var a, b []store.Book
	for _, title := range []string{"a1", "a2", "a3", "a4"} {
		a = append(a, store.Book{Title: title})
	}
	// The second list starts far down, so by score alone all of the first would come before it.
	b = make([]store.Book, 60)
	for i := range b {
		b[i].Title = "filler"
	}
	b = append(b, store.Book{Title: "b1"}, store.Book{Title: "b2"})
	feed := buildFeed([]Recommendation{feedList(1, a...), feedList(2, b...)}, nil, "day")
	for i := 1; i < 5; i++ {
		if feed[i].SourceID == feed[i-1].SourceID {
			t.Fatalf("books %d and %d are both from source %d: %q", i-1, i, feed[i].SourceID, feedTitles(feed))
		}
	}
}

// The shelf has the title of the detail page, the lists a longer one: it is the same book.
func TestFeedLeavesOutAKnownBookUnderItsListTitle(t *testing.T) {
	lists := []Recommendation{
		feedList(1, store.Book{ID: 7, Title: "长夜（精校版）"}, store.Book{ID: 8, Title: "别的书"}),
		feedList(2, store.Book{ID: 9, Title: "长夜（精校版）"}),
	}
	known := []*store.ShelfBook{{Book: store.Book{ID: 7, Title: "长夜"}}}
	if titles := feedTitles(buildFeed(lists, known, "day")); !slices.Equal(titles, []string{"别的书"}) {
		t.Fatalf("feed %q", titles)
	}
}
