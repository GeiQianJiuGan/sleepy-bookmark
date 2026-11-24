package library

import (
	"cmp"
	"context"
	"fmt"
	"hash/fnv"
	"slices"
	"strconv"
	"time"

	"github.com/GeiQianJiuGan/sleepy-bookmark/server/internal/store"
)

const (
	// A book's place n on a source's list is worth 1/(feedRankK+n), and the places on all the
	// lists add up (reciprocal rank fusion): a book that several sources list beats a book
	// that one source puts first.
	feedRankK = 10
	// Books by an author the reader has on the shelf or in the history come earlier.
	feedAuthorBoost = 1.5
	// A book without a cover makes a poor card.
	feedCoverBoost = 1.1
	// The order shifts a little from day to day, so that the feed is not always the same.
	feedJitter = 0.15
	// How far down the order the next book may come from to keep two neighbours from one source.
	feedWindow = 5
)

// FeedBook is a recommended book: one entry per book, however many sources list it.
type FeedBook struct {
	*store.Book
	// Sources is the number of sources that have the book on their list.
	Sources int `json:"sources"`
	// Reason says why the book is here when there is more to it than a place on one list.
	Reason string `json:"reason,omitempty"`
}

// Feed returns a page of the recommendations for a reader and the length of the whole feed:
// the lists of all sources merged into one, without the books the reader already knows.
func (l *Library) Feed(ctx context.Context, userID int64, typ string, offset, limit int) ([]FeedBook, int, error) {
	lists, err := l.rankings(ctx, typ, 0)
	if err != nil {
		return nil, 0, err
	}
	known, err := l.db.Favorites(userID, typ)
	if err != nil {
		return nil, 0, err
	}
	history, err := l.db.History(userID, typ)
	if err != nil {
		return nil, 0, err
	}
	seed := time.Now().Format(time.DateOnly) + "/" + strconv.FormatInt(userID, 10)
	feed := buildFeed(lists, append(known, history...), seed)
	return page(feed, offset, limit), len(feed), nil
}

type feedCandidate struct {
	// book is the copy that stands for the book, rank its place on that source's list.
	book          *store.Book
	rank          int
	title, author string
	sources       map[int64]bool
	score         float64
}

// buildFeed merges the lists. The same seed gives the same order, so that pages line up.
func buildFeed(lists []Recommendation, known []*store.ShelfBook, seed string) []FeedBook {
	read, authors, ids := map[string]bool{}, map[string]bool{}, map[int64]bool{}
	for _, b := range known {
		read[foldText(b.Title)] = true
		ids[b.ID] = true
		if a := foldText(b.Author); a != "" {
			authors[a] = true
		}
	}
	// A list may give a known book another title than its detail page did: that title is
	// read as well, on every list.
	for _, list := range lists {
		for _, b := range list.Books {
			if ids[b.ID] {
				read[foldText(b.Title)] = true
			}
		}
	}

	// One candidate per title and author; a copy without an author joins the title it shares.
	byTitle := map[string][]*feedCandidate{}
	var all []*feedCandidate
	for _, list := range lists {
		for rank, b := range list.Books {
			title, author := foldText(b.Title), foldText(b.Author)
			if title == "" || read[title] {
				continue
			}
			var c *feedCandidate
			for _, x := range byTitle[title] {
				if x.author == "" || author == "" || x.author == author {
					c = x
					break
				}
			}
			if c == nil {
				c = &feedCandidate{book: b, rank: rank, title: title, sources: map[int64]bool{}}
				byTitle[title] = append(byTitle[title], c)
				all = append(all, c)
			}
			if c.author == "" {
				c.author = author
			}
			// A source counts once, with the best place it gives the book.
			if !c.sources[b.SourceID] {
				c.sources[b.SourceID] = true
				c.score += 1 / float64(feedRankK+rank+1)
			}
			// The copy with a cover stands for the book, then the one placed higher.
			if has, had := b.Cover != "", c.book.Cover != ""; has && !had || has == had && rank < c.rank {
				c.book, c.rank = b, rank
			}
		}
	}

	for _, c := range all {
		if authors[c.author] {
			c.score *= feedAuthorBoost
		}
		if c.book.Cover != "" {
			c.score *= feedCoverBoost
		}
		c.score *= 1 + feedJitter*(2*unit(seed+"/"+c.title+"/"+c.author)-1)
	}
	slices.SortStableFunc(all, func(x, y *feedCandidate) int { return cmp.Compare(y.score, x.score) })

	// Take the best book each time, unless one a little further down comes from another source
	// than the book before it.
	out := make([]FeedBook, 0, len(all))
	var last int64
	for len(all) > 0 {
		pick := 0
		for i := range min(feedWindow, len(all)) {
			if all[i].book.SourceID != last {
				pick = i
				break
			}
		}
		c := all[pick]
		all = slices.Delete(all, pick, pick+1)
		last = c.book.SourceID
		fb := FeedBook{Book: c.book, Sources: len(c.sources)}
		switch {
		case authors[c.author]:
			fb.Reason = "读过这位作者"
		case fb.Sources > 1:
			fb.Reason = fmt.Sprintf("%d 个书源上榜", fb.Sources)
		}
		out = append(out, fb)
	}
	return out
}

// unit maps a string to a number in [0, 1).
func unit(s string) float64 {
	h := fnv.New32a()
	h.Write([]byte(s))
	return float64(h.Sum32()) / (1 << 32)
}
