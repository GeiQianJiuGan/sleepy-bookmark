package api_test

import (
	"fmt"
	"net/http"
	"testing"
)

func TestSourceMonitoringMetricsAndCheck(t *testing.T) {
	c := login(t)

	// Run a search so the demo novel source records real response time and valid content.
	var found searchResult
	if code := c.do("GET", qStar, nil, &found); code != http.StatusOK || len(found.Books) != 1 {
		t.Fatalf("search: %d %+v", code, found)
	}

	var report struct {
		Summary struct {
			TotalSources     int     `json:"totalSources"`
			MonitoredSources int     `json:"monitoredSources"`
			TotalRequests    int64   `json:"totalRequests"`
			ValidRequests    int64   `json:"validRequests"`
			InvalidRequests  int64   `json:"invalidRequests"`
			ValidRate        float64 `json:"validRate"`
		} `json:"summary"`
		Sources []struct {
			SourceID  int64   `json:"sourceId"`
			Total     int64   `json:"total"`
			Valid     int64   `json:"valid"`
			Invalid   int64   `json:"invalid"`
			ValidRate float64 `json:"validRate"`
		} `json:"sources"`
	}
	if code := c.do("GET", "/api/sources/metrics", nil, &report); code != http.StatusOK {
		t.Fatalf("metrics: %d", code)
	}
	if report.Summary.TotalRequests < 1 || report.Summary.ValidRequests < 1 || report.Summary.ValidRate != 1 {
		t.Fatalf("unexpected metrics summary: %+v", report.Summary)
	}

	// Active monitor check probes all enabled sources.
	if code := c.do("POST", "/api/sources/monitor/check?q=%E6%98%9F%E6%B2%B3", nil, &report); code != http.StatusOK {
		t.Fatalf("monitor check: %d", code)
	}
	if report.Summary.MonitoredSources < 2 {
		t.Fatalf("expected both demo sources to be monitored after check: %+v", report.Summary)
	}

	// Reset metrics for one source.
	id := c.sources("")[0].ID
	if code := c.do("DELETE", fmt.Sprintf("/api/sources/%d/metrics", id), nil, nil); code != http.StatusNoContent {
		t.Fatalf("reset metrics: %d", code)
	}
}

func TestSourceBundleImportSkipInvalidAndExport(t *testing.T) {
	c := login(t)

	bundle := map[string]any{
		"name": "测试源包",
		"sources": []any{
			map[string]any{"name": "包内源1", "type": "novel", "baseUrl": c.site.URL + "/demo/novel"},
			map[string]any{"name": "", "type": "novel", "baseUrl": "https://invalid.example"},
		},
	}
	var out struct {
		IDs     []int64 `json:"ids"`
		Added   int     `json:"added"`
		Updated int     `json:"updated"`
		Skipped []struct {
			Name  string `json:"name"`
			Error string `json:"error"`
		} `json:"skipped"`
	}
	if code := c.do("POST", "/api/sources?skipInvalid=1", bundle, &out); code != http.StatusOK {
		t.Fatalf("bundle import: %d", code)
	}
	if out.Added != 1 || len(out.Skipped) != 1 {
		t.Fatalf("unexpected bundle import outcome: %+v", out)
	}

	var exported []map[string]any
	if code := c.do("GET", "/api/sources/export", nil, &exported); code != http.StatusOK {
		t.Fatalf("export: %d", code)
	}
	if len(exported) < 3 {
		t.Fatalf("expected at least 3 exported sources, got %d", len(exported))
	}
}

func TestAutoDisableLowAccuracyAndRecoverOnManualAnalyze(t *testing.T) {
	c := login(t)
	c.addCopy("flaky", "http://127.0.0.1:1")

	var flakyID int64
	for _, s := range c.sources("") {
		if s.Name == "flaky" {
			flakyID = s.ID
		}
	}
	if flakyID == 0 {
		t.Fatal("flaky source not found")
	}

	// Manual batch analysis probes flaky -> 0% valid rate -> auto-disabled.
	var batch struct {
		AutoDisabled []string `json:"autoDisabled"`
		AutoEnabled  []string `json:"autoEnabled"`
	}
	if code := c.do("POST", "/api/sources/monitor/check?q=%E6%98%9F%E6%B2%B3", nil, &batch); code != http.StatusOK {
		t.Fatalf("batch check: %d", code)
	}
	if len(batch.AutoDisabled) != 1 || batch.AutoDisabled[0] != "flaky" {
		t.Fatalf("expected flaky to be auto-disabled, got %+v", batch)
	}

	var list []struct {
		ID           int64 `json:"id"`
		Name         string `json:"name"`
		Enabled      bool `json:"enabled"`
		AutoDisabled bool `json:"autoDisabled"`
		Rule         map[string]any `json:"rule"`
	}
	c.do("GET", "/api/sources", nil, &list)
	var flakyRule map[string]any
	for _, s := range list {
		if s.ID == flakyID {
			if s.Enabled || !s.AutoDisabled {
				t.Fatalf("expected flaky disabled+autoDisabled, got enabled=%v autoDisabled=%v", s.Enabled, s.AutoDisabled)
			}
			flakyRule = s.Rule
		}
	}

	// Fix flaky's baseUrl so it works again, then run manual batch analysis: it should auto-enable.
	flakyRule["baseUrl"] = c.site.URL + "/demo/novel"
	if code := c.do("PUT", fmt.Sprintf("/api/sources/%d", flakyID), flakyRule, nil); code != http.StatusNoContent {
		t.Fatalf("update flaky rule: %d", code)
	}
	batch.AutoDisabled = nil
	batch.AutoEnabled = nil
	if code := c.do("POST", "/api/sources/monitor/check?q=%E6%98%9F%E6%B2%B3", nil, &batch); code != http.StatusOK {
		t.Fatalf("recovery batch check: %d", code)
	}
	if len(batch.AutoEnabled) != 1 || batch.AutoEnabled[0] != "flaky" {
		t.Fatalf("expected flaky to auto-enable after recovery, got %+v", batch)
	}
	list = nil
	c.do("GET", "/api/sources", nil, &list)
	for _, s := range list {
		if s.ID == flakyID && (!s.Enabled || s.AutoDisabled) {
			t.Fatalf("expected flaky enabled after recovery, got enabled=%v autoDisabled=%v", s.Enabled, s.AutoDisabled)
		}
	}
}
