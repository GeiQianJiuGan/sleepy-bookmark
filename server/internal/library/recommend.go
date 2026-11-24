package library

import (
	"context"
	"strconv"
	"sync"
	"time"

	"github.com/GeiQianJiuGan/sleepy-bookmark/server/internal/source"
	"github.com/GeiQianJiuGan/sleepy-bookmark/server/internal/store"
)

const (
	recommendCount = 10
	// rankMax is how much of a source's list is kept for the ranking and the feed.
	rankMax          = 100
	recommendMaxAge  = 30 * time.Minute
	recommendTimeout = 10 * time.Second
	// A list that could not be read is not asked for again for this long, so that a dead site
	// does not slow down every visit of the page.
	recommendRetryAfter = 5 * time.Minute
)

// Recommendation is one source's recommendation list, or a part of it.
type Recommendation struct {
	SourceID   int64         `json:"sourceId"`
	SourceName string        `json:"sourceName"`
	Books      []*store.Book `json:"books"`
}

type recommendEntry struct {
	// rule is the source rule the books were read with; an edited rule makes the entry stale.
	rule string
	// at is when the books were read, failedAt when reading them last failed.
	at, failedAt time.Time
	books        []*store.Book
}

type recommendCache struct {
	mu sync.Mutex
	m  map[int64]recommendEntry
}

// get returns the list last read for the source, and whether to read it again.
func (c *recommendCache) get(row *store.SourceRow) (books []*store.Book, due bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.m[row.ID]
	if !ok || e.rule != row.Rule {
		return nil, true
	}
	return e.books, time.Since(e.at) >= recommendMaxAge && time.Since(e.failedAt) >= recommendRetryAfter
}

// put stores a list that was read, or with ok false notes that reading it failed.
func (c *recommendCache) put(row *store.SourceRow, books []*store.Book, ok bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.m == nil {
		c.m = map[int64]recommendEntry{}
	}
	e := c.m[row.ID]
	if e.rule != row.Rule {
		e = recommendEntry{rule: row.Rule}
	}
	if ok {
		e.books, e.at = books, time.Now()
	} else {
		e.failedAt = time.Now()
	}
	c.m[row.ID] = e
}

// rankings returns the recommendation list of every enabled source of the given type that has
// one, or of the one source when sourceID is set. A source whose list cannot be read is left
// out, or keeps showing its previous list: recommendations are optional and their failures are
// not worth a message.
func (l *Library) rankings(ctx context.Context, typ string, sourceID int64) ([]Recommendation, error) {
	rows, err := l.db.Sources(typ, true)
	if err != nil {
		return nil, err
	}
	lists := make([][]*store.Book, len(rows))
	visits := make(chan struct{}, maxVisits)
	var wg sync.WaitGroup
	for i, row := range rows {
		if sourceID != 0 && row.ID != sourceID {
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			visits <- struct{}{}
			defer func() { <-visits }()
			lists[i] = l.recommend(ctx, row)
		}()
	}
	wg.Wait()

	out := []Recommendation{}
	for i, row := range rows {
		if len(lists[i]) > 0 {
			out = append(out, Recommendation{row.ID, row.Name, lists[i]})
		}
	}
	return out, nil
}

// Recommend returns the first books of each list, see rankings.
func (l *Library) Recommend(ctx context.Context, typ string, sourceID int64) ([]Recommendation, error) {
	out, err := l.rankings(ctx, typ, sourceID)
	for i := range out {
		out[i].Books = out[i].Books[:min(len(out[i].Books), recommendCount)]
	}
	return out, err
}

// Rank returns a page of one source's list and the length of the whole list.
func (l *Library) Rank(ctx context.Context, sourceID int64, offset, limit int) (Recommendation, int, error) {
	row, err := l.db.Source(sourceID)
	if err != nil {
		return Recommendation{}, 0, err
	}
	out := Recommendation{SourceID: row.ID, SourceName: row.Name, Books: []*store.Book{}}
	if !row.Enabled {
		return out, 0, nil
	}
	books := l.recommend(ctx, row)
	out.Books = page(books, offset, limit)
	return out, len(books), nil
}

func page[T any](list []T, offset, limit int) []T {
	offset = min(max(offset, 0), len(list))
	return list[offset:min(offset+max(limit, 0), len(list))]
}

func (l *Library) recommend(ctx context.Context, row *store.SourceRow) []*store.Book {
	old, due := l.recommends.get(row)
	if !due {
		return old
	}
	src, err := parseSource(row)
	if err != nil || src.Recommend == nil {
		return nil
	}
	// Requests that arrive together share one visit of the site.
	v, _, _ := l.flight.Do("recommend:"+strconv.FormatInt(row.ID, 10), func() (any, error) {
		// The request that started the visit may go away; the others still want the list.
		ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), recommendTimeout)
		defer cancel()
		books, err := l.readRecommend(ctx, row, src)
		if err != nil || len(books) == 0 {
			l.recommends.put(row, nil, false)
			return old, nil
		}
		l.recommends.put(row, books, true)
		return books, nil
	})
	return v.([]*store.Book)
}

func (l *Library) readRecommend(ctx context.Context, row *store.SourceRow, src *source.Source) ([]*store.Book, error) {
	start := time.Now()
	infos, err := l.engine.Recommend(ctx, src)
	l.monitor.Record(row.ID, ActionRecommend, time.Since(start), len(infos) > 0, err)
	if err != nil {
		return nil, err
	}
	books := make([]*store.Book, 0, min(len(infos), rankMax))
	seen := map[string]bool{}
	for _, info := range infos {
		if len(books) == rankMax {
			break
		}
		if seen[info.URL] {
			continue
		}
		seen[info.URL] = true
		b := &store.Book{SourceID: row.ID, SourceName: row.Name, Type: row.Type, URL: info.URL,
			Title: info.Title, Author: info.Author, Cover: info.Cover, Intro: info.Intro, Latest: info.Latest}
		if b.ID, err = l.db.UpsertBook(b); err != nil {
			return nil, err
		}
		books = append(books, b)
	}
	return books, nil
}
