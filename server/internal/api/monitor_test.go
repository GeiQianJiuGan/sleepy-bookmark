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
