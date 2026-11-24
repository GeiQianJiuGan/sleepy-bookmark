package api_test

import (
	"strings"
	"testing"
)

func TestASourceOfANameAndAnAddress(t *testing.T) {
	c := login(t)
	rule := map[string]any{"name": "只有地址", "type": "novel", "baseUrl": c.site.URL + "/demo/novel/"}

	// Tested without a keyword: the book comes from the home page.
	var report struct {
		FailedStep, Error, SearchURL string
		ChapterCount                 int
		Content                      struct{ Text string }
	}
	if code := c.do("POST", "/api/sources/test", rule, &report); code != 200 {
		t.Fatalf("test: %d", code)
	}
	if report.FailedStep != "" || !strings.Contains(report.SearchURL, "{{key}}") || report.ChapterCount == 0 || report.Content.Text == "" {
		t.Fatalf("report: %+v", report)
	}
	if code := c.do("POST", "/api/sources/test?q=星", rule, &report); code != 200 || report.FailedStep != "" {
		t.Fatalf("test with a keyword: %d %+v", code, report)
	}

	if code := c.do("POST", "/api/sources", rule, nil); code != 200 {
		t.Fatalf("save: %d", code)
	}
	var list []struct {
		Name string
		Auto bool
		Rule struct{ Search, Toc struct{ List string } }
	}
	c.do("GET", "/api/sources?type=novel", nil, &list)
	for _, s := range list {
		if s.Auto != (s.Name == "只有地址") {
			t.Errorf("%s: auto = %v", s.Name, s.Auto)
		}
		if s.Name == "只有地址" && (s.Rule.Search.List != "auto" || s.Rule.Toc.List != "auto") {
			t.Errorf("stored rule: %+v", s.Rule)
		}
	}

	// A site that cannot be recognised is reported, with the step that failed.
	rule["baseUrl"] = c.site.URL + "/nothing-here"
	if code := c.do("POST", "/api/sources/test", rule, &report); code != 200 || report.FailedStep == "" || report.Error == "" {
		t.Fatalf("unrecognised: %d %+v", code, report)
	}
}
