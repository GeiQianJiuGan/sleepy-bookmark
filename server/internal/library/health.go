package library

import (
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/GeiQianJiuGan/sleepy-bookmark/server/internal/source"
)

const (
	// A source that was unreachable this many times in a row is left out of searches for a
	// while, so that one dead site does not make every search wait for its timeout.
	pauseAfter = 3
	pauseFor   = 10 * time.Minute
)

// Health is what the server remembers, in memory, about a source whose search keeps failing.
type Health struct {
	Failures    int    `json:"failures"`
	LastError   string `json:"lastError"`
	LastErrorAt int64  `json:"lastErrorAt"`
	PausedUntil int64  `json:"pausedUntil"`
}

type healthBook struct {
	mu sync.Mutex
	m  map[int64]*Health
}

// note records the outcome of a search in a source. Only searches are counted, because the
// pause applies to searches: a chapter the site refuses to serve says nothing about its search
// page. Of the failures only those of the site itself count, not a rule that matches nothing.
func (h *healthBook) note(sourceID int64, err error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if err == nil {
		delete(h.m, sourceID)
		return
	}
	if !source.Unreachable(err) {
		return
	}
	if h.m == nil {
		h.m = map[int64]*Health{}
	}
	s := h.m[sourceID]
	if s == nil {
		s = &Health{}
		h.m[sourceID] = s
	}
	now := time.Now()
	s.Failures++
	s.LastError, s.LastErrorAt = err.Error(), now.Unix()
	if s.Failures >= pauseAfter {
		s.PausedUntil = now.Add(pauseFor).Unix()
	}
}

func (h *healthBook) get(sourceID int64) *Health {
	h.mu.Lock()
	defer h.mu.Unlock()
	if s := h.m[sourceID]; s != nil {
		c := *s
		return &c
	}
	return nil
}

func (h *healthBook) paused(sourceID int64) bool {
	s := h.get(sourceID)
	return s != nil && time.Now().Unix() < s.PausedUntil
}

func (h *healthBook) reset(sourceID int64) {
	h.mu.Lock()
	delete(h.m, sourceID)
	h.mu.Unlock()
}

// Health returns the recent failures of a source, nil if it has been working.
func (l *Library) Health(sourceID int64) *Health { return l.health.get(sourceID) }

// ResetHealth forgets a source's failures, for example after its rule was edited.
func (l *Library) ResetHealth(sourceID int64) { l.health.reset(sourceID) }

// sameText compares titles and author names ignoring spacing, punctuation and case.
func sameText(a, b string) bool {
	norm := func(s string) string {
		return strings.Map(func(r rune) rune {
			if unicode.IsSpace(r) || unicode.IsPunct(r) || unicode.IsSymbol(r) {
				return -1
			}
			return unicode.ToLower(r)
		}, s)
	}
	a, b = norm(a), norm(b)
	return a != "" && a == b
}
