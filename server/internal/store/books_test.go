package store

import (
	"path/filepath"
	"slices"
	"testing"
)

func entries(urls ...string) []TocEntry {
	out := make([]TocEntry, len(urls))
	for i, u := range urls {
		out[i] = TocEntry{Title: "chapter " + u, URL: u}
	}
	return out
}

func newBook(t *testing.T) (*DB, int64) {
	t.Helper()
	db, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	srcID, err := db.CreateSource("s", "novel", "{}")
	if err != nil {
		t.Fatal(err)
	}
	bookID, err := db.UpsertBook(&Book{SourceID: srcID, Type: "novel", URL: "book", Title: "b"})
	if err != nil {
		t.Fatal(err)
	}
	return db, bookID
}

// When the site moves its chapters to new URLs, a chapter is recognised by its title so that
// reading progress still points at it; only its cached content is dropped.
func TestReplaceTocKeepsChapterIDsWhenURLsChange(t *testing.T) {
	db, bookID := newBook(t)
	if _, err := db.ReplaceToc(bookID, []TocEntry{{"one", "/old/1"}, {"two", "/old/2"}}); err != nil {
		t.Fatal(err)
	}
	before, _ := db.Chapters(bookID)
	if err := db.MarkChapterCached(before[1].ID, "{}"); err != nil {
		t.Fatal(err)
	}

	stale, err := db.ReplaceToc(bookID, []TocEntry{{"one", "/new/1"}, {"two", "/new/2"}, {"three", "/new/3"}})
	if err != nil {
		t.Fatal(err)
	}
	after, _ := db.Chapters(bookID)
	if len(after) != 3 || after[0].ID != before[0].ID || after[1].ID != before[1].ID {
		t.Fatalf("chapter ids changed: before %+v %+v, after %+v %+v", before[0], before[1], after[0], after[1])
	}
	if after[1].URL != "/new/2" || after[1].Cached || !slices.Equal(stale, []int64{before[1].ID}) {
		t.Fatalf("chapter two: %+v, stale %v", after[1], stale)
	}
}

func TestReplaceTocKeepsChapterIDsByURL(t *testing.T) {
	db, bookID := newBook(t)

	// urls returns the chapter URLs in order, ids maps URL to chapter id.
	state := func() (urls []string, ids map[string]int64) {
		t.Helper()
		list, err := db.Chapters(bookID)
		if err != nil {
			t.Fatal(err)
		}
		ids = map[string]int64{}
		for i, c := range list {
			if c.Idx != i {
				t.Fatalf("chapter %q has idx %d at position %d", c.URL, c.Idx, i)
			}
			urls = append(urls, c.URL)
			ids[c.URL] = c.ID
		}
		return
	}

	if _, err := db.ReplaceToc(bookID, entries("a", "b", "c")); err != nil {
		t.Fatal(err)
	}
	_, first := state()
	if err := db.MarkChapterCached(first["b"], "{}"); err != nil {
		t.Fatal(err)
	}

	// The source lists a new chapter first: every existing chapter moves down one position.
	stale, err := db.ReplaceToc(bookID, entries("x", "a", "b", "c"))
	if err != nil {
		t.Fatal(err)
	}
	urls, ids := state()
	if !slices.Equal(urls, []string{"x", "a", "b", "c"}) || len(stale) != 0 {
		t.Fatalf("after insert: urls %v, stale %v", urls, stale)
	}
	for _, u := range []string{"a", "b", "c"} {
		if ids[u] != first[u] {
			t.Errorf("chapter %q changed id from %d to %d", u, first[u], ids[u])
		}
	}
	if c, _ := db.Chapter(first["b"]); !c.Cached {
		t.Error("chapter b lost its cached flag")
	}

	// Reorder and drop the cached chapter: its id is reported so the content file can be removed.
	stale, err = db.ReplaceToc(bookID, entries("c", "a"))
	if err != nil {
		t.Fatal(err)
	}
	urls, ids = state()
	if !slices.Equal(urls, []string{"c", "a"}) || ids["a"] != first["a"] || ids["c"] != first["c"] {
		t.Fatalf("after reorder: urls %v, ids %v (first %v)", urls, ids, first)
	}
	if !slices.Equal(stale, []int64{first["b"]}) {
		t.Fatalf("stale = %v, want [%d]", stale, first["b"])
	}
}

// A cached chapter was read up to the next chapter's URL. When the source later lists a
// chapter in between, the cached text may run into that chapter and must be read again.
func TestReplaceTocDropsCacheWhenTheNextChapterChanges(t *testing.T) {
	db, bookID := newBook(t)
	if _, err := db.ReplaceToc(bookID, entries("a", "c")); err != nil {
		t.Fatal(err)
	}
	before, _ := db.Chapters(bookID)
	for _, c := range before {
		if err := db.MarkChapterCached(c.ID, "{}"); err != nil {
			t.Fatal(err)
		}
	}

	stale, err := db.ReplaceToc(bookID, entries("a", "b", "c"))
	if err != nil {
		t.Fatal(err)
	}
	after, _ := db.Chapters(bookID)
	if after[0].ID != before[0].ID || after[0].Cached || !slices.Equal(stale, []int64{before[0].ID}) {
		t.Fatalf("chapter a: %+v, stale %v", after[0], stale)
	}
	if after[2].ID != before[1].ID || !after[2].Cached {
		t.Fatalf("chapter c should keep its cache: %+v", after[2])
	}
}

func TestMarkChapterCachedOnlyForTheCurrentRule(t *testing.T) {
	db, bookID := newBook(t) // its source has the rule "{}"
	if _, err := db.ReplaceToc(bookID, entries("a")); err != nil {
		t.Fatal(err)
	}
	list, _ := db.Chapters(bookID)
	id := list[0].ID

	// Fetched with a rule that has been replaced since: the result must not count as cached.
	if err := db.MarkChapterCached(id, `{"old":true}`); err != nil {
		t.Fatal(err)
	}
	if c, _ := db.Chapter(id); c.Cached {
		t.Fatal("content fetched with an outdated rule was marked cached")
	}
	if err := db.MarkChapterCached(id, "{}"); err != nil {
		t.Fatal(err)
	}
	if c, _ := db.Chapter(id); !c.Cached {
		t.Fatal("content fetched with the current rule was not marked cached")
	}
}

// Two chapters share a title and the site moves to new URLs while dropping the first of them:
// the remaining one cannot be told apart by title, so no id is carried over to it.
func TestReplaceTocDoesNotGuessBetweenChaptersWithTheSameTitle(t *testing.T) {
	db, bookID := newBook(t)
	if _, err := db.ReplaceToc(bookID, []TocEntry{{"notice", "/old/1"}, {"one", "/old/2"}, {"notice", "/old/3"}}); err != nil {
		t.Fatal(err)
	}
	before, _ := db.Chapters(bookID)

	if _, err := db.ReplaceToc(bookID, []TocEntry{{"one", "/new/2"}, {"notice", "/new/3"}}); err != nil {
		t.Fatal(err)
	}
	after, _ := db.Chapters(bookID)
	if after[0].ID != before[1].ID {
		t.Errorf("chapter with a unique title changed id from %d to %d", before[1].ID, after[0].ID)
	}
	if after[1].ID == before[0].ID || after[1].ID == before[2].ID {
		t.Errorf("an ambiguous title was matched to an existing chapter: %+v", after[1])
	}
}
