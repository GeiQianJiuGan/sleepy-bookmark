package library

import (
	"cmp"
	"context"
	"log"
	"math"
	"slices"
	"sync"
	"time"

	"github.com/GeiQianJiuGan/sleepy-bookmark/server/internal/source"
	"github.com/GeiQianJiuGan/sleepy-bookmark/server/internal/store"
)

const (
	ActionSearch    = "search"
	ActionRecommend = "recommend"
	ActionDetail    = "detail"
	ActionToc       = "toc"
	ActionContent   = "content"
	ActionImage     = "image"

	StatusValid = "valid"
	StatusEmpty = "empty"
	StatusError = "error"

	latencyWindowSize = 100
)

// ActionMetrics summarizes requests, content validity and response latency for one source action.
type ActionMetrics struct {
	Total         int64   `json:"total"`
	Valid         int64   `json:"valid"`
	Invalid       int64   `json:"invalid"`
	Empty         int64   `json:"empty"`
	Error         int64   `json:"error"`
	ValidRate     float64 `json:"validRate"`
	InvalidRate   float64 `json:"invalidRate"`
	AvgLatencyMs  int64   `json:"avgLatencyMs"`
	LastLatencyMs int64   `json:"lastLatencyMs"`
}

// SourceMetrics summarizes response time and valid/invalid content ratios for one source.
type SourceMetrics struct {
	SourceID      int64                    `json:"sourceId"`
	SourceName    string                   `json:"sourceName,omitempty"`
	SourceType    string                   `json:"sourceType,omitempty"`
	Total         int64                    `json:"total"`
	Valid         int64                    `json:"valid"`
	Invalid       int64                    `json:"invalid"`
	Empty         int64                    `json:"empty"`
	Error         int64                    `json:"error"`
	ValidRate     float64                  `json:"validRate"`
	InvalidRate   float64                  `json:"invalidRate"`
	AvgLatencyMs  int64                    `json:"avgLatencyMs"`
	LastLatencyMs int64                    `json:"lastLatencyMs"`
	MinLatencyMs  int64                    `json:"minLatencyMs"`
	MaxLatencyMs  int64                    `json:"maxLatencyMs"`
	P95LatencyMs  int64                    `json:"p95LatencyMs"`
	LastStatus    string                   `json:"lastStatus"`
	LastError     string                   `json:"lastError,omitempty"`
	LastErrorKind string                   `json:"lastErrorKind,omitempty"`
	LastCheckedAt int64                    `json:"lastCheckedAt"`
	Actions       map[string]ActionMetrics `json:"actions,omitempty"`
}

// MonitorSummary aggregates health, latency and valid content ratio across all sources.
type MonitorSummary struct {
	TotalSources     int     `json:"totalSources"`
	MonitoredSources int     `json:"monitoredSources"`
	HealthySources   int     `json:"healthySources"`
	DegradedSources  int     `json:"degradedSources"`
	UnhealthySources int     `json:"unhealthySources"`
	TotalRequests    int64   `json:"totalRequests"`
	ValidRequests    int64   `json:"validRequests"`
	InvalidRequests  int64   `json:"invalidRequests"`
	ValidRate        float64 `json:"validRate"`
	InvalidRate      float64 `json:"invalidRate"`
	AvgLatencyMs     int64   `json:"avgLatencyMs"`
}

type sourceBucket struct {
	actions   map[string]*store.SourceMetricRow
	latencies []int64 // ring buffer of most recent response times in ms
	lastAt    int64
	lastMs    int64
	lastSt    string
	lastErr   string
	lastKind  string
}

type sourceMonitor struct {
	db *store.DB

	mu      sync.Mutex
	buckets map[int64]*sourceBucket
}

func newSourceMonitor(db *store.DB) *sourceMonitor {
	m := &sourceMonitor{
		db:      db,
		buckets: map[int64]*sourceBucket{},
	}
	if db == nil {
		return m
	}
	rows, err := db.LoadSourceMetrics()
	if err != nil {
		log.Printf("load source metrics: %v", err)
		return m
	}
	for _, r := range rows {
		b := m.bucketLocked(r.SourceID)
		cp := *r
		b.actions[r.Action] = &cp
		if r.LastCheckedAt >= b.lastAt {
			b.lastAt = r.LastCheckedAt
			b.lastMs = r.LastLatency
			b.lastSt = r.LastStatus
			b.lastErr = r.LastError
			b.lastKind = r.LastErrorKind
		}
	}
	return m
}

func (m *sourceMonitor) bucketLocked(sourceID int64) *sourceBucket {
	b := m.buckets[sourceID]
	if b == nil {
		b = &sourceBucket{actions: map[string]*store.SourceMetricRow{}}
		m.buckets[sourceID] = b
	}
	return b
}

// Record notes one interaction with a source: how long it took, whether it returned usable content,
// and any error. Empty responses and errors both count as invalid content, tracked separately so
// callers can tell a broken rule / empty search from a network failure.
func (m *sourceMonitor) Record(sourceID int64, action string, elapsed time.Duration, valid bool, err error) {
	if m == nil || sourceID <= 0 {
		return
	}
	ms := max(elapsed.Milliseconds(), 0)
	now := time.Now().Unix()

	status := StatusValid
	errText, errKind := "", ""
	if err != nil {
		status = StatusError
		errText = err.Error()
		errKind = source.Kind(err)
	} else if !valid {
		status = StatusEmpty
	}

	m.mu.Lock()
	b := m.bucketLocked(sourceID)
	row := b.actions[action]
	if row == nil {
		row = &store.SourceMetricRow{SourceID: sourceID, Action: action}
		b.actions[action] = row
	}
	row.Total++
	switch status {
	case StatusValid:
		row.Valid++
	case StatusEmpty:
		row.Empty++
	case StatusError:
		row.Error++
	}
	row.TotalLatency += ms
	row.LastLatency = ms
	if row.Total == 1 || ms < row.MinLatency {
		row.MinLatency = ms
	}
	if ms > row.MaxLatency {
		row.MaxLatency = ms
	}
	row.LastStatus = status
	row.LastError = errText
	row.LastErrorKind = errKind
	row.LastCheckedAt = now

	if len(b.latencies) >= latencyWindowSize {
		copy(b.latencies, b.latencies[1:])
		b.latencies[len(b.latencies)-1] = ms
	} else {
		b.latencies = append(b.latencies, ms)
	}
	b.lastAt = now
	b.lastMs = ms
	b.lastSt = status
	b.lastErr = errText
	b.lastKind = errKind

	snap := *row
	m.mu.Unlock()

	if m.db != nil {
		if err := m.db.UpsertSourceMetric(&snap); err != nil {
			log.Printf("persist source metrics (%d/%s): %v", sourceID, action, err)
		}
	}
}

func (m *sourceMonitor) Get(sourceID int64) *SourceMetrics {
	if m == nil || sourceID <= 0 {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	b := m.buckets[sourceID]
	if b == nil || len(b.actions) == 0 {
		return nil
	}
	return buildSourceMetrics(sourceID, b)
}

func (m *sourceMonitor) Reset(sourceID int64) error {
	if m == nil || sourceID <= 0 {
		return nil
	}
	m.mu.Lock()
	delete(m.buckets, sourceID)
	m.mu.Unlock()
	if m.db != nil {
		return m.db.DeleteSourceMetrics(sourceID)
	}
	return nil
}

func buildSourceMetrics(sourceID int64, b *sourceBucket) *SourceMetrics {
	out := &SourceMetrics{
		SourceID:      sourceID,
		LastLatencyMs: b.lastMs,
		LastStatus:    b.lastSt,
		LastError:     b.lastErr,
		LastErrorKind: b.lastKind,
		LastCheckedAt: b.lastAt,
		Actions:       make(map[string]ActionMetrics, len(b.actions)),
	}
	var totalLatency int64
	minSet := false
	for action, r := range b.actions {
		inv := r.Empty + r.Error
		am := ActionMetrics{
			Total:         r.Total,
			Valid:         r.Valid,
			Invalid:       inv,
			Empty:         r.Empty,
			Error:         r.Error,
			ValidRate:     rate(r.Valid, r.Total),
			InvalidRate:   rate(inv, r.Total),
			LastLatencyMs: r.LastLatency,
		}
		if r.Total > 0 {
			am.AvgLatencyMs = r.TotalLatency / r.Total
		}
		out.Actions[action] = am

		out.Total += r.Total
		out.Valid += r.Valid
		out.Empty += r.Empty
		out.Error += r.Error
		totalLatency += r.TotalLatency
		if r.Total > 0 {
			if !minSet || r.MinLatency < out.MinLatencyMs {
				out.MinLatencyMs = r.MinLatency
				minSet = true
			}
			if r.MaxLatency > out.MaxLatencyMs {
				out.MaxLatencyMs = r.MaxLatency
			}
		}
	}
	out.Invalid = out.Empty + out.Error
	out.ValidRate = rate(out.Valid, out.Total)
	out.InvalidRate = rate(out.Invalid, out.Total)
	if out.Total > 0 {
		out.AvgLatencyMs = totalLatency / out.Total
	}
	if len(b.latencies) > 0 {
		out.P95LatencyMs = percentile(b.latencies, 0.95)
	} else {
		out.P95LatencyMs = out.AvgLatencyMs
	}
	return out
}

func rate(part, total int64) float64 {
	if total <= 0 {
		return 0
	}
	return math.Round(float64(part)/float64(total)*10000) / 10000
}

func percentile(values []int64, p float64) int64 {
	if len(values) == 0 {
		return 0
	}
	cp := slices.Clone(values)
	slices.Sort(cp)
	idx := int(math.Ceil(p*float64(len(cp)))) - 1
	idx = min(max(idx, 0), len(cp)-1)
	return cp[idx]
}

// Metrics returns the collected response-time and content-validity stats for a source, or nil if
// the source has not been queried yet.
func (l *Library) Metrics(sourceID int64) *SourceMetrics { return l.monitor.Get(sourceID) }

// ResetMetrics clears stored monitoring statistics for one source.
func (l *Library) ResetMetrics(sourceID int64) error {
	l.health.reset(sourceID)
	return l.monitor.Reset(sourceID)
}

// AllMetrics returns monitoring metrics for every configured source plus an overall summary.
func (l *Library) AllMetrics(typ string) ([]*SourceMetrics, MonitorSummary, error) {
	rows, err := l.db.Sources(typ, false)
	if err != nil {
		return nil, MonitorSummary{}, err
	}
	summary := MonitorSummary{TotalSources: len(rows)}
	list := make([]*SourceMetrics, 0, len(rows))
	var latencySum int64
	for _, r := range rows {
		m := l.monitor.Get(r.ID)
		if m == nil {
			m = &SourceMetrics{SourceID: r.ID}
		}
		m.SourceName = r.Name
		m.SourceType = r.Type
		list = append(list, m)
		if m.Total > 0 {
			summary.MonitoredSources++
			summary.TotalRequests += m.Total
			summary.ValidRequests += m.Valid
			summary.InvalidRequests += m.Invalid
			latencySum += m.AvgLatencyMs * m.Total
			switch {
			case m.ValidRate >= 0.8:
				summary.HealthySources++
			case m.ValidRate >= 0.5:
				summary.DegradedSources++
			default:
				summary.UnhealthySources++
			}
		}
	}
	summary.ValidRate = rate(summary.ValidRequests, summary.TotalRequests)
	summary.InvalidRate = rate(summary.InvalidRequests, summary.TotalRequests)
	if summary.TotalRequests > 0 {
		summary.AvgLatencyMs = latencySum / summary.TotalRequests
	}
	return list, summary, nil
}

// CheckSource actively probes one source (using its recommend list if configured, otherwise a
// lightweight search) so latency and valid/invalid content ratios can be measured on demand.
func (l *Library) CheckSource(ctx context.Context, sourceID int64, keyword string) (*SourceMetrics, error) {
	row, err := l.db.Source(sourceID)
	if err != nil {
		return nil, err
	}
	l.probeSource(ctx, row, keyword)
	m := l.monitor.Get(row.ID)
	if m == nil {
		m = &SourceMetrics{SourceID: row.ID}
	}
	m.SourceName = row.Name
	m.SourceType = row.Type
	return m, nil
}

// CheckAllSources actively probes every enabled source (or those of one type) concurrently.
func (l *Library) CheckAllSources(ctx context.Context, typ, keyword string) ([]*SourceMetrics, MonitorSummary, error) {
	rows, err := l.db.Sources(typ, true)
	if err != nil {
		return nil, MonitorSummary{}, err
	}
	sem := make(chan struct{}, 4)
	var wg sync.WaitGroup
	for _, row := range rows {
		wg.Add(1)
		go func() {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				return
			}
			l.probeSource(ctx, row, keyword)
		}()
	}
	wg.Wait()
	return l.AllMetrics(typ)
}

func (l *Library) probeSource(ctx context.Context, row *store.SourceRow, keyword string) {
	src, err := parseSource(row)
	if err != nil {
		l.monitor.Record(row.ID, ActionSearch, 0, false, err)
		return
	}
	if src.Recommend != nil && keyword == "" {
		pctx, cancel := context.WithTimeout(ctx, recommendTimeout)
		defer cancel()
		start := time.Now()
		infos, err := l.engine.Recommend(pctx, src)
		l.monitor.Record(row.ID, ActionRecommend, time.Since(start), len(infos) > 0, err)
		return
	}
	q := cmp.Or(keyword, "斗")
	pctx, cancel := context.WithTimeout(ctx, searchTimeout)
	defer cancel()
	start := time.Now()
	books, err := l.engine.Search(pctx, src, q, 1)
	l.health.note(row.ID, err)
	l.monitor.Record(row.ID, ActionSearch, time.Since(start), len(books) > 0, err)
}
