// Package library combines sources, the database and the disk cache into the reading features.
package library

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"slices"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"

	"github.com/GeiQianJiuGan/sleepy-bookmark/server/internal/cache"
	"github.com/GeiQianJiuGan/sleepy-bookmark/server/internal/config"
	"github.com/GeiQianJiuGan/sleepy-bookmark/server/internal/source"
	"github.com/GeiQianJiuGan/sleepy-bookmark/server/internal/store"
)

const (
	searchTimeout = 20 * time.Second
	fetchTimeout  = 60 * time.Second
	// tocMaxAge is how old a chapter list may be before opening the book refreshes it.
	tocMaxAge = 6 * time.Hour
)

type Library struct {
	cfg     config.Config
	db      *store.DB
	disk    *cache.Disk
	engine  *source.Engine
	flight  singleflight.Group
	health  healthBook
	monitor *sourceMonitor

	recommends recommendCache

	// background limits how many prefetch jobs run at once.
	background chan struct{}

	taskMu sync.Mutex
	tasks  []*Task
	taskID int64
	queue  chan *Task
}

func New(cfg config.Config, db *store.DB, disk *cache.Disk) *Library {
	disk.KeptMax = cfg.KeptMaxMB << 20
	return &Library{
		cfg:        cfg,
		db:         db,
		disk:       disk,
		engine:     &source.Engine{Fetcher: source.NewFetcher()},
		monitor:    newSourceMonitor(db),
		background: make(chan struct{}, 2),
		queue:      make(chan *Task, 100),
	}
}

func (l *Library) Engine() *source.Engine { return l.engine }

func (l *Library) loadSource(id int64) (*source.Source, error) {
	row, err := l.db.Source(id)
	if err != nil {
		return nil, err
	}
	return parseSource(row)
}

// parseSource reads the rule of a stored source. One that came with a subscribed list was
// written by someone else, and is kept to public addresses, subscribed to still or not.
func parseSource(row *store.SourceRow) (*source.Source, error) {
	src, err := source.ParseSource([]byte(row.Rule))
	if src != nil {
		src.Public = row.Listed
	}
	return src, err
}

type SearchError struct {
	Source string `json:"source"`
	Error  string `json:"error"`
	// Kind is a source.Kind* value, or "paused" for a source skipped after repeated failures.
	Kind string `json:"kind"`
}

var errPaused = errors.New("连续多次无法访问，暂时跳过")

// maxVisits is how many sources one search or one round of rankings visits at a time.
const maxVisits = 32

var errNotReached = errors.New("书源太多，这次搜索没有轮到它，可以只选这个书源再搜")

// SearchOptions narrows a search.
type SearchOptions struct {
	// SourceID searches one source only; zero searches every enabled source.
	SourceID int64
	// Exact keeps only the books whose title is the keyword.
	Exact bool
}

// Search queries the enabled sources of the given type concurrently. The sites decide what
// matches a keyword; the result is ordered by how well the title and author match it, and with
// Exact is reduced to the books of that title.
func (l *Library) Search(ctx context.Context, typ, key string, page int, opt SearchOptions) ([]*store.Book, []SearchError, error) {
	books, errs, err := l.search(ctx, typ, key, page, func(r *store.SourceRow) bool {
		return opt.SourceID == 0 || r.ID == opt.SourceID
	})
	if err != nil {
		return nil, nil, err
	}
	if opt.Exact {
		books = slices.DeleteFunc(books, func(b *store.Book) bool { return !exactTitle(b.Title, key) })
	} else {
		slices.SortStableFunc(books, func(a, b *store.Book) int {
			return cmp.Compare(relevance(a, key), relevance(b, key))
		})
	}
	return books, errs, nil
}

// exactTitle reports whether a book is the one the reader named. Spacing, punctuation and case
// are not compared, as sites and readers write them differently; a title of punctuation only
// has nothing else and is compared as written.
func exactTitle(title, key string) bool {
	return sameText(title, key) || strings.TrimSpace(title) == strings.TrimSpace(key)
}

// relevance ranks a search result: the smaller, the better the book matches the keyword.
func relevance(b *store.Book, key string) int {
	title, author, key := foldText(b.Title), foldText(b.Author), foldText(key)
	switch {
	case key == "":
		return 0
	case title == key:
		return 0
	case strings.HasPrefix(title, key):
		return 1
	case strings.Contains(title, key):
		return 2
	case strings.Contains(author, key):
		return 3
	}
	return 4
}

func (l *Library) search(ctx context.Context, typ, key string, page int, want func(*store.SourceRow) bool) ([]*store.Book, []SearchError, error) {
	rows, err := l.db.Sources(typ, true)
	if err != nil {
		return nil, nil, err
	}
	rows = slices.DeleteFunc(rows, func(r *store.SourceRow) bool { return !want(r) })
	ctx, cancel := context.WithTimeout(ctx, searchTimeout)
	defer cancel()

	type result struct {
		row   *store.SourceRow
		books []source.BookInfo
		err   error
	}
	results := make([]result, len(rows))
	// A subscribed list can bring thousands of sources; they are visited a few at a time.
	visits := make(chan struct{}, maxVisits)
	var wg sync.WaitGroup
	for i, row := range rows {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i].row = row
			if l.health.paused(row.ID) {
				results[i].err = errPaused
				return
			}
			select {
			case visits <- struct{}{}:
				defer func() { <-visits }()
			case <-ctx.Done():
				results[i].err = errNotReached
				return
			}
			src, err := parseSource(row)
			if err == nil {
				start := time.Now()
				results[i].books, err = l.engine.Search(ctx, src, key, page)
				l.monitor.Record(row.ID, ActionSearch, time.Since(start), len(results[i].books) > 0, err)
				l.health.note(row.ID, err)
			}
			results[i].err = err
		}()
	}
	wg.Wait()

	books := []*store.Book{}
	errs := []SearchError{}
	for _, r := range results {
		if r.err != nil {
			kind := source.Kind(r.err)
			if r.err == errPaused {
				kind = "paused"
			}
			errs = append(errs, SearchError{r.row.Name, r.err.Error(), kind})
			continue
		}
		for _, info := range r.books {
			b := &store.Book{SourceID: r.row.ID, SourceName: r.row.Name, Type: r.row.Type, URL: info.URL,
				Title: info.Title, Author: info.Author, Cover: info.Cover, Intro: info.Intro, Latest: info.Latest}
			if b.ID, err = l.db.UpsertBook(b); err != nil {
				return nil, nil, err
			}
			books = append(books, b)
		}
	}
	return books, errs, nil
}

// Book returns a book, loading its detail page the first time it is opened.
func (l *Library) Book(ctx context.Context, id int64) (*store.Book, error) {
	b, err := l.db.Book(id)
	if err != nil || b.DetailAt > 0 {
		return b, err
	}
	src, err := l.loadSource(b.SourceID)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, fetchTimeout)
	defer cancel()
	start := time.Now()
	info, err := l.engine.Detail(ctx, src, b.URL)
	validDetail := info.Title != "" || info.Author != "" || info.Cover != "" || info.Intro != "" || info.Latest != ""
	l.monitor.Record(b.SourceID, ActionDetail, time.Since(start), validDetail, err)
	if err != nil {
		// The search result is still usable without the detail page.
		log.Printf("detail %q: %v", b.Title, err)
		return b, nil
	}
	b.Title = pick(info.Title, b.Title)
	b.Author = pick(info.Author, b.Author)
	b.Cover = pick(info.Cover, b.Cover)
	b.Intro = pick(info.Intro, b.Intro)
	b.Latest = pick(info.Latest, b.Latest)
	b.TocURL = info.TocURL
	return b, l.db.SetBookDetail(b)
}

func pick(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// Chapters returns the chapter list, fetching it when missing, stale or when refresh is set.
func (l *Library) Chapters(ctx context.Context, bookID int64, refresh bool) ([]*store.Chapter, error) {
	b, err := l.Book(ctx, bookID)
	if err != nil {
		return nil, err
	}
	if refresh || time.Since(time.Unix(b.TocAt, 0)) > tocMaxAge {
		if _, err := l.refreshToc(ctx, b); err != nil {
			// Unless a refresh was asked for, the catalog read earlier is better than an error.
			list, dbErr := l.db.Chapters(bookID)
			if refresh || dbErr != nil || len(list) == 0 {
				return nil, err
			}
			log.Printf("refresh toc %q: %v", b.Title, err)
			return list, nil
		}
	}
	return l.db.Chapters(bookID)
}

// refreshToc re-reads the chapter list and returns the chapters added since the last read.
func (l *Library) refreshToc(ctx context.Context, b *store.Book) ([]*store.Chapter, error) {
	v, err, _ := l.flight.Do(fmt.Sprintf("toc:%d", b.ID), func() (any, error) {
		src, err := l.loadSource(b.SourceID)
		if err != nil {
			return nil, err
		}
		ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), fetchTimeout)
		defer cancel()
		start := time.Now()
		toc, err := l.engine.Toc(ctx, src, pick(b.TocURL, b.URL))
		l.monitor.Record(b.SourceID, ActionToc, time.Since(start), len(toc) > 0, err)
		if err != nil {
			return nil, err
		}
		before, err := l.db.Chapters(b.ID)
		if err != nil {
			return nil, err
		}
		entries := make([]store.TocEntry, len(toc))
		for i, c := range toc {
			entries[i] = store.TocEntry{Title: c.Title, URL: c.URL}
		}
		stale, err := l.db.ReplaceToc(b.ID, entries)
		if err != nil {
			return nil, err
		}
		for _, id := range stale {
			l.disk.RemoveChapter(b.ID, id)
		}
		after, err := l.db.Chapters(b.ID)
		if err != nil || len(before) == 0 {
			return []*store.Chapter(nil), err
		}
		known := make(map[int64]bool, len(before))
		for _, c := range before {
			known[c.ID] = true
		}
		var added []*store.Chapter
		for _, c := range after {
			if !known[c.ID] {
				added = append(added, c)
			}
		}
		return added, nil
	})
	if err != nil {
		return nil, err
	}
	return v.([]*store.Chapter), nil
}

// ChapterContent is a chapter with its payload and neighbours.
type ChapterContent struct {
	*store.Chapter
	source.Content
	// Origins are the addresses of Images at the source, for clients that can load them directly.
	Origins  []string `json:"origins,omitempty"`
	SourceID int64    `json:"-"`
	PrevID   int64    `json:"prevId"`
	NextID   int64    `json:"nextId"`
}

// Chapter returns a chapter's content from the disk cache or the source, then warms the next chapters.
func (l *Library) Chapter(ctx context.Context, id int64) (*ChapterContent, error) {
	ch, err := l.db.Chapter(id)
	if err != nil {
		return nil, err
	}
	b, err := l.db.Book(ch.BookID)
	if err != nil {
		return nil, err
	}
	out := &ChapterContent{Chapter: ch, SourceID: b.SourceID}
	if prev, err := l.db.ChapterAt(ch.BookID, ch.Idx-1); err == nil {
		out.PrevID = prev.ID
	}
	next, err := l.db.ChapterAt(ch.BookID, ch.Idx+1)
	if err == nil {
		out.NextID = next.ID
	} else {
		next = nil
	}
	if out.Content, err = l.content(ctx, b, ch, next); err != nil {
		return nil, err
	}
	ch.Cached = true
	l.prefetch(b, ch.Idx)
	return out, nil
}

func (l *Library) content(ctx context.Context, b *store.Book, ch, next *store.Chapter) (source.Content, error) {
	var c source.Content
	if ch.Cached && l.disk.LoadChapter(b.ID, ch.ID, &c) {
		return c, nil
	}
	v, err, _ := l.flight.Do(fmt.Sprintf("ch:%d", ch.ID), func() (any, error) {
		row, err := l.db.Source(b.SourceID)
		if err != nil {
			return nil, err
		}
		src, err := parseSource(row)
		if err != nil {
			return nil, err
		}
		// Detached from the request so a reader who swipes away does not waste the fetch.
		ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), fetchTimeout)
		defer cancel()
		stop := ""
		if next != nil {
			stop = next.URL
		}
		start := time.Now()
		c, err := l.engine.Content(ctx, src, ch.URL, stop)
		l.monitor.Record(b.SourceID, ActionContent, time.Since(start), c.Text != "" || len(c.Images) > 0, err)
		if err != nil {
			return nil, err
		}
		if err := l.disk.SaveChapter(b.ID, ch.ID, c); err != nil {
			return nil, err
		}
		return c, l.db.MarkChapterCached(ch.ID, ch.URL, row.Rule)
	})
	if err != nil {
		return c, err
	}
	return v.(source.Content), nil
}

// prefetch caches the chapters following idx in the background.
func (l *Library) prefetch(b *store.Book, idx int) {
	if l.cfg.PrefetchAhead <= 0 {
		return
	}
	select {
	case l.background <- struct{}{}:
	default:
		return
	}
	go func() {
		defer func() { <-l.background }()
		for i := idx + 1; i <= idx+l.cfg.PrefetchAhead; i++ {
			ch, err := l.db.ChapterAt(b.ID, i)
			if err != nil {
				return
			}
			if err := l.cacheChapter(context.Background(), b, ch, false); err != nil {
				log.Printf("prefetch %q #%d: %v", b.Title, i, err)
				return
			}
		}
	}()
}

// cacheChapter makes sure a chapter (and optionally a comic chapter's images) is on disk. The
// pages of a comic on somebody's shelf are kept for good while there is room for them.
func (l *Library) cacheChapter(ctx context.Context, b *store.Book, ch *store.Chapter, withImages bool) error {
	next, err := l.db.ChapterAt(b.ID, ch.Idx+1)
	if err != nil {
		next = nil
	}
	c, err := l.content(ctx, b, ch, next)
	if err != nil || !withImages || len(c.Images) == 0 {
		return err
	}
	keep := l.db.Favorited(b.ID)
	var failed error
	for _, img := range c.Images {
		// Without room among the kept images, one in the cache is as far as it goes.
		if keep && l.disk.KeepImage(img) || l.disk.HasImage(img) {
			continue
		}
		if _, err := l.fetchImage(ctx, b.SourceID, img, keep); err != nil {
			// A page that the site no longer has does not cost the chapter its other pages.
			if ctx.Err() != nil || source.Kind(err) != source.KindNotFound {
				return err
			}
			failed = err
		}
	}
	return failed
}

// Image returns a source image from the disk, or from the source and then caches it.
func (l *Library) Image(ctx context.Context, sourceID int64, url string) ([]byte, error) {
	if data, ok := l.disk.LoadImage(url); ok {
		return data, nil
	}
	return l.fetchImage(ctx, sourceID, url, false)
}

func (l *Library) fetchImage(ctx context.Context, sourceID int64, url string, keep bool) ([]byte, error) {
	v, err, _ := l.flight.Do("img:"+url, func() (any, error) {
		src, err := l.loadSource(sourceID)
		if err != nil {
			return nil, err
		}
		ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), fetchTimeout)
		defer cancel()
		start := time.Now()
		data, err := l.engine.Fetcher.Image(ctx, src, url)
		l.monitor.Record(sourceID, ActionImage, time.Since(start), len(data) > 0, err)
		if err != nil {
			return nil, err
		}
		return data, l.disk.SaveImage(url, data, keep)
	})
	if err != nil {
		return nil, err
	}
	if keep {
		// A reader may have asked for the same image at the same time, which caches it.
		l.disk.KeepImage(url)
	}
	return v.([]byte), nil
}

// TestSource runs search, toc and the first chapter of a rule without touching the database.
// Without a keyword it takes the book to try from the rule's list of recommendations.
func (l *Library) TestSource(ctx context.Context, src *source.Source, key string) map[string]any {
	ctx, cancel := context.WithTimeout(ctx, fetchTimeout)
	defer cancel()
	started := time.Now()
	report := map[string]any{}
	fail := func(step string, err error) map[string]any {
		report["failedStep"], report["error"] = step, err.Error()
		report["latencyMs"] = time.Since(started).Milliseconds()
		return report
	}
	var books []source.BookInfo
	if key != "" {
		found, err := l.engine.Search(ctx, src, key, 1)
		if err == nil && len(found) == 0 {
			err = fmt.Errorf("搜索 %q 没有结果", key)
		}
		if err != nil {
			return fail("search", err)
		}
		books = found
	} else {
		// A source that cannot be searched is of no use, whatever else works.
		searchURL, err := l.engine.SearchURL(ctx, src)
		if err != nil {
			return fail("search", err)
		}
		report["searchUrl"] = searchURL
		if books, err = l.engine.Recommend(ctx, src); err == nil && len(books) == 0 {
			err = errors.New("没有在首页识别到书籍列表，请填一个搜索关键词再试")
		}
		if err != nil {
			return fail("recommend", err)
		}
	}
	report["searchCount"], report["book"] = len(books), books[0]
	detail, err := l.engine.Detail(ctx, src, books[0].URL)
	if err != nil {
		return fail("detail", err)
	}
	report["detail"] = detail
	toc, err := l.engine.Toc(ctx, src, detail.TocURL)
	if err != nil {
		return fail("toc", err)
	}
	report["chapterCount"], report["firstChapter"] = len(toc), toc[0]
	stop := ""
	if len(toc) > 1 {
		stop = toc[1].URL
	}
	c, err := l.engine.Content(ctx, src, toc[0].URL, stop)
	if err != nil {
		return fail("content", err)
	}
	if text := []rune(c.Text); len(text) > 200 {
		c.Text = string(text[:200]) + "…"
	}
	report["content"] = c
	report["latencyMs"] = time.Since(started).Milliseconds()
	return report
}

// SeedSource inserts a source unless one with the same name exists.
func (l *Library) SeedSource(src source.Source) error {
	if _, err := l.db.SourceByName(src.Name); err == nil {
		return nil
	}
	raw, _ := json.Marshal(src)
	_, err := l.db.CreateSource(src.Name, src.Type, string(raw), 0)
	return err
}

// Alternatives looks for the same book in the other enabled sources, same author first.
func (l *Library) Alternatives(ctx context.Context, bookID int64) ([]*store.Book, error) {
	b, err := l.db.Book(bookID)
	if err != nil {
		return nil, err
	}
	found, _, err := l.search(ctx, b.Type, b.Title, 1, func(r *store.SourceRow) bool { return r.ID != b.SourceID })
	if err != nil {
		return nil, err
	}
	out := []*store.Book{}
	for _, x := range found {
		if sameText(x.Title, b.Title) {
			out = append(out, x)
		}
	}
	slices.SortStableFunc(out, func(x, y *store.Book) int {
		return cmp.Compare(btoi(sameText(y.Author, b.Author)), btoi(sameText(x.Author, b.Author)))
	})
	return out, nil
}

func btoi(b bool) int {
	if b {
		return 1
	}
	return 0
}

// SwitchSource moves a reader from a book to the same book in another source: the shelf entry
// and the chapter being read, found by its title or else by its position in the catalog.
func (l *Library) SwitchSource(ctx context.Context, userID, fromID, toID int64) error {
	from, err := l.db.ShelfBook(userID, fromID)
	if err != nil {
		return err
	}
	// Reading the catalog again first proves that the new source works for this book right now;
	// without the refresh a catalog stored earlier would hide that it is down too.
	chapters, err := l.Chapters(ctx, toID, true)
	if err != nil {
		return err
	}
	to, err := l.db.ShelfBook(userID, toID)
	if err != nil {
		return err
	}
	// The most recent reading position wins: a newer bookmark in the destination stays, as long
	// as the chapter it points at is still in the catalog.
	keep := to.Progress != nil && from.Progress != nil && to.Progress.UpdatedAt > from.Progress.UpdatedAt &&
		slices.ContainsFunc(chapters, func(c *store.Chapter) bool { return c.ID == to.Progress.ChapterID })
	if p := from.Progress; p != nil && len(chapters) > 0 && !keep {
		// Same title, and if several chapters have it, the one nearest to the old position;
		// without a title match, the chapter at the old position.
		target := chapters[min(p.ChapterIdx, len(chapters)-1)]
		best := -1
		for _, c := range chapters {
			if d := max(c.Idx-p.ChapterIdx, p.ChapterIdx-c.Idx); sameText(c.Title, p.ChapterTitle) && (best < 0 || d < best) {
				target, best = c, d
			}
		}
		// The position inside the chapter does not carry over: pages differ between sources.
		if err := l.db.SaveProgress(userID, toID, target.ID, 0); err != nil {
			return err
		}
	}
	if from.Progress != nil {
		if err := l.db.RemoveHistory(userID, fromID); err != nil {
			return err
		}
	}
	if from.Favorite {
		if err := l.AddFavorite(ctx, userID, toID); err != nil {
			return err
		}
		return l.RemoveFavorite(userID, fromID)
	}
	return nil
}

// AddFavorite puts a book on the user's shelf. A comic is then saved on the server, pages
// included, so that reading it later does not depend on the source site.
func (l *Library) AddFavorite(ctx context.Context, userID, bookID int64) error {
	b, err := l.db.Book(bookID)
	if err != nil {
		return err
	}
	if err := l.db.AddFavorite(userID, bookID); err != nil {
		return err
	}
	if b.Type == source.TypeComic {
		if _, err := l.CacheBook(ctx, bookID); err != nil {
			log.Printf("save favorite %q: %v", b.Title, err)
		}
	}
	return nil
}

// RemoveFavorite takes a book off the user's shelf. Once nobody has a comic on their shelf, its
// saved pages go back to the image cache, where the least recently used ones are evicted.
func (l *Library) RemoveFavorite(userID, bookID int64) error {
	if err := l.db.RemoveFavorite(userID, bookID); err != nil {
		return err
	}
	if b, err := l.db.Book(bookID); err == nil && b.Type == source.TypeComic {
		l.ReleaseKept()
	}
	return nil
}

// ReleaseKept hands back the kept images that no comic on a shelf has in a saved chapter any
// more: the book left the shelf, its source was deleted, or its catalog dropped the chapter.
// Going by what the shelves need now, rather than by the book that changed, also leaves alone
// the pages that another comic on a shelf shares.
func (l *Library) ReleaseKept() {
	ids, err := l.db.FavoriteBookIDs()
	if err != nil {
		return
	}
	want := map[string]bool{}
	for _, id := range ids {
		b, err := l.db.Book(id)
		if err != nil || b.Type != source.TypeComic {
			continue
		}
		chapters, err := l.db.Chapters(id)
		if err != nil {
			return
		}
		for _, ch := range chapters {
			var c source.Content
			if ch.Cached && l.disk.LoadChapter(id, ch.ID, &c) {
				for _, img := range c.Images {
					want[cache.ImageName(img)] = true
				}
			}
		}
	}
	if n := l.disk.ReleaseKept(want); n > 0 {
		log.Printf("kept images: released %d that no shelf needs", n)
	}
}
