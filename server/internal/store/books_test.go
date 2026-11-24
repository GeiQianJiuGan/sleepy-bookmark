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

func TestReplaceTocKeepsChapterIDsByURL(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	srcID, err := db.CreateSource("s", "novel", "{}")
	if err != nil {
		t.Fatal(err)
	}
	bookID, err := db.UpsertBook(&Book{SourceID: srcID, Type: "novel", URL: "book", Title: "b"})
	if err != nil {
		t.Fatal(err)
	}

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
	if err := db.SetChapterCached(first["b"], true); err != nil {
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
