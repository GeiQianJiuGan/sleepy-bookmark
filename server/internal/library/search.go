package library

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/GeiQianJiuGan/sleepy-bookmark/server/internal/source"
	"github.com/GeiQianJiuGan/sleepy-bookmark/server/internal/store"
)

const (
	// maxSearchVisits is how many sources are searched at a time, over all searches.
	maxSearchVisits = 64
	// A search answers once every source has, or after searchQuick once a book was found, or
	// after searchPatience whatever was found. The sources that have not answered go on in
	// the background, and searching again shows what they found.
	searchQuick    = 2 * time.Second
	searchPatience = 5 * time.Second
	// A search waits this long for its turn at a source before it is given up.
	searchQueueMax = 30 * time.Second
	// What a source found is kept this long. A failure is not kept: the next search asks again,
	// and a source that keeps failing is paused.
	searchMaxAge   = 10 * time.Minute
	searchCacheMax = 4000
)

var errPending = errors.New("还在搜索，稍后再搜一次可以看到它的结果")

// visit is one source's answer to one search, or the search under way.
type visit struct {
	done  chan struct{}
	at    time.Time
	books []*store.Book
	err   error
}

func (v *visit) finished() bool {
	select {
	case <-v.done:
		return true
	default:
		return false
	}
}

type searchCache struct {
	mu     sync.Mutex
	m      map[string]*visit
	visits chan struct{}
}

// visit returns the answer of a source to a search: the one it gave a moment ago, the one
// that is under way, or a new one that goes on whether or not the request waits for it.
func (l *Library) visit(row *store.SourceRow, key string, page int) *visit {
	c := &l.searches
	id := fmt.Sprintf("%d/%d/%s/%s", row.ID, page, row.Rule, key)
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.m == nil {
		c.m = map[string]*visit{}
		c.visits = make(chan struct{}, maxSearchVisits)
	}
	if v := c.m[id]; v != nil {
		if !v.finished() {
			return v
		}
		if v.err == nil && time.Since(v.at) < searchMaxAge {
			return v
		}
	}
	if len(c.m) >= searchCacheMax {
		for k, v := range c.m {
			if v.finished() && (v.err != nil || time.Since(v.at) >= searchMaxAge) {
				delete(c.m, k)
			}
		}
	}
	v := &visit{done: make(chan struct{})}
	if len(c.m) < searchCacheMax {
		c.m[id] = v
	}
	go func() {
		defer close(v.done)
		defer func() { v.at = time.Now() }()
		queue := time.NewTimer(searchQueueMax)
		defer queue.Stop()
		select {
		case c.visits <- struct{}{}:
			defer func() { <-c.visits }()
		case <-queue.C:
			v.err = errNotReached
			return
		}
		src, err := parseSource(row)
		if err != nil {
			v.err = err
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), searchTimeout)
		defer cancel()
		start := time.Now()
		infos, err := l.engine.Search(ctx, src, key, page)
		l.monitor.Record(row.ID, ActionSearch, time.Since(start), len(infos) > 0, err)
		l.health.note(row.ID, err)
		if err != nil {
			v.err = err
			return
		}
		for _, info := range infos {
			b := &store.Book{SourceID: row.ID, SourceName: row.Name, Type: row.Type, URL: info.URL,
				Title: info.Title, Author: info.Author, Cover: info.Cover, Intro: info.Intro, Latest: info.Latest}
			if b.ID, err = l.db.UpsertBook(b); err != nil {
				v.books, v.err = nil, err
				return
			}
			v.books = append(v.books, b)
		}
	}()
	return v
}

// search asks the sources that want accepts. hit tells which books the reader is after: the
// search answers early only once one of them was found.
func (l *Library) search(ctx context.Context, typ, key string, page int, want func(*store.SourceRow) bool, hit func(*store.Book) bool) ([]*store.Book, []SearchError, error) {
	rows, err := l.db.Sources(typ, true)
	if err != nil {
		return nil, nil, err
	}
	rows = slices.DeleteFunc(rows, func(r *store.SourceRow) bool { return !want(r) })
	// The sources that answered searches well and fast before are asked first.
	order := make(map[int64]int64, len(rows))
	for _, row := range rows {
		order[row.ID] = l.searchOrder(row.ID)
	}
	slices.SortStableFunc(rows, func(a, b *store.SourceRow) int { return cmp.Compare(order[a.ID], order[b.ID]) })

	visits := make([]*visit, len(rows))
	finished := make(chan *visit, len(rows))
	pending := 0
	for i, row := range rows {
		if l.health.paused(row.ID) {
			continue
		}
		v := l.visit(row, key, page)
		visits[i] = v
		pending++
		go func() {
			<-v.done
			finished <- v
		}()
	}
	quick, patience := time.NewTimer(searchQuick), time.NewTimer(searchPatience)
	defer quick.Stop()
	defer patience.Stop()
	found, early := false, false
	for pending > 0 && !(early && found) {
		select {
		case v := <-finished:
			pending--
			found = found || slices.ContainsFunc(v.books, hit)
		case <-quick.C:
			// One source alone is what the reader asked for, however long it takes.
			early = len(rows) > 1
		case <-patience.C:
			early, found = len(rows) > 1, len(rows) > 1
		case <-ctx.Done():
			early, found = true, true
		}
	}

	books := []*store.Book{}
	errs := []SearchError{}
	for i, v := range visits {
		switch {
		case v == nil:
			errs = append(errs, SearchError{rows[i].Name, errPaused.Error(), "paused"})
		case !v.finished():
			errs = append(errs, SearchError{rows[i].Name, errPending.Error(), "pending"})
		case v.err != nil:
			errs = append(errs, SearchError{rows[i].Name, v.err.Error(), source.Kind(v.err)})
		default:
			// A copy each, as the caller signs the cover in place.
			for _, b := range v.books {
				c := *b
				books = append(books, &c)
			}
		}
	}
	return books, errs, nil
}

// searchOrder is smaller for a source to ask earlier: one whose searches found books, by how
// fast it was, then one that was not asked yet, then one whose searches found nothing.
func (l *Library) searchOrder(sourceID int64) int64 {
	const unknown, poor = 1 << 40, 1 << 41
	m := l.monitor.Get(sourceID)
	if m == nil {
		return unknown
	}
	a, ok := m.Actions[ActionSearch]
	switch {
	case !ok || a.Total == 0:
		return unknown
	case a.Valid == 0:
		return poor
	}
	return a.AvgLatencyMs
}
