package library

import (
	"context"
	"log"
	"time"

	"github.com/GeiQianJiuGan/sleepy-bookmark/server/internal/source"
)

const maxTasks = 50

// Task is a whole-book cache job.
type Task struct {
	ID     int64  `json:"id"`
	BookID int64  `json:"bookId"`
	Title  string `json:"title"`
	Status string `json:"status"` // queued / running / done / failed
	Total  int    `json:"total"`
	Done   int    `json:"done"`
	Failed int    `json:"failed"`
	Error  string `json:"error,omitempty"`
}

// CacheBook queues a job that caches every chapter of a book on the server.
func (l *Library) CacheBook(ctx context.Context, bookID int64) (*Task, error) {
	b, err := l.Book(ctx, bookID)
	if err != nil {
		return nil, err
	}
	l.taskMu.Lock()
	defer l.taskMu.Unlock()
	for _, t := range l.tasks {
		if t.BookID == bookID && (t.Status == "queued" || t.Status == "running") {
			snap := *t
			return &snap, nil
		}
	}
	l.taskID++
	t := &Task{ID: l.taskID, BookID: bookID, Title: b.Title, Status: "queued"}
	select {
	case l.queue <- t:
	default:
		t.Status, t.Error = "failed", "任务队列已满"
	}
	l.tasks = append(l.tasks, t)
	if len(l.tasks) > maxTasks {
		l.tasks = l.tasks[len(l.tasks)-maxTasks:]
	}
	snap := *t
	return &snap, nil
}

// Tasks returns a snapshot of recent cache jobs, newest first.
func (l *Library) Tasks() []Task {
	l.taskMu.Lock()
	defer l.taskMu.Unlock()
	out := make([]Task, 0, len(l.tasks))
	for i := len(l.tasks) - 1; i >= 0; i-- {
		out = append(out, *l.tasks[i])
	}
	return out
}

func (l *Library) update(t *Task, fn func(*Task)) {
	l.taskMu.Lock()
	fn(t)
	l.taskMu.Unlock()
}

func (l *Library) runTask(ctx context.Context, t *Task) {
	l.update(t, func(t *Task) { t.Status = "running" })
	fail := func(err error) { l.update(t, func(t *Task) { t.Status, t.Error = "failed", err.Error() }) }

	b, err := l.Book(ctx, t.BookID)
	if err != nil {
		fail(err)
		return
	}
	chapters, err := l.Chapters(ctx, t.BookID, false)
	if err != nil {
		fail(err)
		return
	}
	l.update(t, func(t *Task) { t.Total = len(chapters) })
	for _, ch := range chapters {
		if ctx.Err() != nil {
			fail(ctx.Err())
			return
		}
		err := l.cacheChapter(ctx, b, ch, true)
		l.update(t, func(t *Task) {
			if err != nil {
				t.Failed++
				t.Error = err.Error()
			} else {
				t.Done++
			}
		})
	}
	l.update(t, func(t *Task) { t.Status = "done" })
}

// Run processes cache jobs and periodically refreshes favorited books until ctx is cancelled.
func (l *Library) Run(ctx context.Context) {
	refresh := time.NewTicker(l.cfg.RefreshInterval)
	defer refresh.Stop()
	evict := time.NewTicker(time.Hour)
	defer evict.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case t := <-l.queue:
			l.runTask(ctx, t)
		case <-refresh.C:
			l.refreshFavorites(ctx)
		case <-evict.C:
			l.ReleaseKept()
			if n := l.disk.EvictImages(l.cfg.ImgCacheMaxMB << 20); n > 0 {
				log.Printf("image cache: evicted %d files", n)
			}
		}
	}
}

// refreshFavorites re-reads the chapter list of every favorited book and caches the new chapters.
func (l *Library) refreshFavorites(ctx context.Context) {
	ids, err := l.db.FavoriteBookIDs()
	if err != nil {
		log.Printf("refresh favorites: %v", err)
		return
	}
	for _, id := range ids {
		if ctx.Err() != nil {
			return
		}
		b, err := l.Book(ctx, id)
		if err != nil {
			continue
		}
		added, err := l.refreshToc(ctx, b)
		if err != nil {
			log.Printf("refresh %q: %v", b.Title, err)
			continue
		}
		for _, ch := range added {
			if err := l.cacheChapter(ctx, b, ch, b.Type == source.TypeComic); err != nil {
				log.Printf("cache %q #%d: %v", b.Title, ch.Idx, err)
				break
			}
		}
	}
}
