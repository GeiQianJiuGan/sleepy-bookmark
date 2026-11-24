package library

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/GeiQianJiuGan/sleepy-bookmark/server/internal/source"
	"github.com/GeiQianJiuGan/sleepy-bookmark/server/internal/store"
)

func TestSourceMonitorMetricsAndPersistence(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "metrics.db")
	db, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	sid, err := db.CreateSource("测试源", "comic", `{"name":"测试源","type":"comic","baseUrl":"https://example.com"}`, 0)
	if err != nil {
		t.Fatal(err)
	}

	mon := newSourceMonitor(db)
	mon.Record(sid, ActionSearch, 120*time.Millisecond, true, nil)
	mon.Record(sid, ActionSearch, 200*time.Millisecond, false, nil)
	mon.Record(sid, ActionToc, 300*time.Millisecond, true, nil)
	mon.Record(sid, ActionContent, 500*time.Millisecond, false, &source.FetchError{Kind: source.KindTimeout, Err: errors.New("timeout")})

	got := mon.Get(sid)
	if got == nil {
		t.Fatal("expected metrics, got nil")
	}
	if got.Total != 4 || got.Valid != 2 || got.Invalid != 2 || got.Empty != 1 || got.Error != 1 {
		t.Fatalf("unexpected counts: %+v", got)
	}
	if got.ValidRate != 0.5 || got.InvalidRate != 0.5 {
		t.Fatalf("unexpected rates: valid=%v invalid=%v", got.ValidRate, got.InvalidRate)
	}
	if got.AvgLatencyMs != 280 || got.MinLatencyMs != 120 || got.MaxLatencyMs != 500 || got.LastLatencyMs != 500 {
		t.Fatalf("unexpected latency stats: %+v", got)
	}
	if got.LastStatus != StatusError || got.LastErrorKind != source.KindTimeout {
		t.Fatalf("unexpected last status: %+v", got)
	}

	// Reload from SQLite to confirm persistence across server restarts.
	reloaded := newSourceMonitor(db)
	got2 := reloaded.Get(sid)
	if got2 == nil || got2.Total != 4 || got2.Valid != 2 || got2.Invalid != 2 || got2.AvgLatencyMs != 280 {
		t.Fatalf("reloaded metrics mismatch: %+v", got2)
	}

	if err := reloaded.Reset(sid); err != nil {
		t.Fatal(err)
	}
	if reloaded.Get(sid) != nil {
		t.Fatal("expected nil after reset")
	}

	// At exactly 50% (2/4), source remains enabled; dropping below 50% (2/5 = 40%) auto-disables it.
	mon2 := newSourceMonitor(db)
	mon2.Record(sid, ActionSearch, 100*time.Millisecond, true, nil)
	mon2.Record(sid, ActionSearch, 100*time.Millisecond, true, nil)
	mon2.Record(sid, ActionSearch, 100*time.Millisecond, false, nil)
	mon2.Record(sid, ActionSearch, 100*time.Millisecond, false, nil)
	row, err := db.Source(sid)
	if err != nil || !row.Enabled || row.AutoDisabled {
		t.Fatalf("expected source to stay enabled at 50%%, got row=%+v err=%v", row, err)
	}
	mon2.Record(sid, ActionSearch, 100*time.Millisecond, false, nil)
	row, err = db.Source(sid)
	if err != nil || row.Enabled || !row.AutoDisabled {
		t.Fatalf("expected source to be auto-disabled below 50%%, got row=%+v err=%v", row, err)
	}
	// Manual toggle clears autoDisabled.
	if err := db.SetSourceEnabled(sid, true); err != nil {
		t.Fatal(err)
	}
	row, err = db.Source(sid)
	if err != nil || !row.Enabled || row.AutoDisabled {
		t.Fatalf("expected manual enable to clear autoDisabled, got row=%+v err=%v", row, err)
	}
}
