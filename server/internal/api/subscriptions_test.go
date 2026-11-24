package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/GeiQianJiuGan/sleepy-bookmark/server/internal/api"
)

type subscriptionResult struct {
	Subscription struct {
		ID       int64
		Name     string
		URL      string
		SyncedAt int64
		Error    string
		Sources  int
	}
	Added, Updated int
	Skipped        []struct{ Name, Error string }
}

// listServer serves whatever list is stored in it, the way a raw file on a code host is served.
func listServer(t *testing.T) (*httptest.Server, *atomic.Pointer[string]) {
	t.Helper()
	var body atomic.Pointer[string]
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/big.json":
			fmt.Fprint(w, "[", strings.Repeat(" ", 2<<20), "]")
		case body.Load() == nil:
			http.NotFound(w, r)
		default:
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			fmt.Fprint(w, *body.Load())
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &body
}

func TestASubscriptionImportsAListAndSyncsItAgain(t *testing.T) {
	c := login(t)
	srv, body := listServer(t)
	site := c.site.URL + "/demo/novel"
	set := func(list ...any) {
		raw, _ := json.Marshal(list)
		text := string(raw)
		body.Store(&text)
	}
	before := len(c.sources(""))

	// Two sources of nothing but a name and an address, and one entry that is no source.
	set(map[string]any{"name": "甲", "type": "novel", "baseUrl": site},
		map[string]any{"name": "乙", "type": "novel", "baseUrl": site},
		map[string]any{"bookSourceName": "别的格式", "bookSourceUrl": site})
	var res subscriptionResult
	in := map[string]string{"url": srv.URL + "/lists/mine.json"}
	if code := c.do("POST", "/api/subscriptions", in, &res); code != 200 {
		t.Fatalf("subscribe: %d", code)
	}
	if res.Added != 2 || res.Updated != 0 || len(res.Skipped) != 1 || res.Skipped[0].Error == "" {
		t.Fatalf("first import: %+v", res)
	}
	if s := res.Subscription; s.Name != "mine" || s.Sources != 2 || s.SyncedAt == 0 || s.Error != "" {
		t.Fatalf("subscription: %+v", s)
	}
	if got := len(c.sources("")); got != before+2 {
		t.Fatalf("sources: %d, want %d", got, before+2)
	}
	var first int64
	for _, s := range c.sources("") {
		if s.Name == "甲" {
			first = s.ID
		}
	}

	// The list changes: 甲 gets a rule of its own, 乙 is gone, 丙 is new.
	set(map[string]any{"name": "甲", "type": "novel", "baseUrl": site, "rateLimitMs": 5},
		map[string]any{"name": "丙", "type": "novel", "baseUrl": site})
	// Subscribing to the same address again syncs the subscription instead of adding one.
	if code := c.do("POST", "/api/subscriptions", in, &res); code != 200 {
		t.Fatalf("subscribe again: %d", code)
	}
	if res.Added != 1 || res.Updated != 1 || res.Subscription.Sources != 3 {
		t.Fatalf("second import: %+v", res)
	}
	var subs []struct {
		ID      int64
		Sources int
	}
	if c.do("GET", "/api/subscriptions", nil, &subs); len(subs) != 1 || subs[0].ID != res.Subscription.ID {
		t.Fatalf("subscriptions: %+v", subs)
	}
	names := map[string]sourceInfo{}
	for _, s := range c.sources("") {
		names[s.Name] = s
	}
	// 甲 is the same source with the new rule; 乙 stays, with whatever is on shelves from it.
	if names["甲"].ID != first || names["甲"].Rule["rateLimitMs"] != float64(5) || names["乙"].ID == 0 || names["丙"].ID == 0 {
		t.Fatalf("after sync: %+v", names)
	}

	// A sync that fails keeps the sources and says what went wrong.
	broken := "<html>not a list</html>"
	body.Store(&broken)
	path := fmt.Sprintf("/api/subscriptions/%d", res.Subscription.ID)
	if code := c.do("POST", path+"/sync", nil, nil); code != http.StatusBadRequest {
		t.Fatalf("sync of a page: %d", code)
	}
	var one []struct{ Error string }
	if c.do("GET", "/api/subscriptions", nil, &one); len(one) != 1 || one[0].Error == "" {
		t.Fatalf("error not kept: %+v", one)
	}
	if got := len(c.sources("")); got != before+3 {
		t.Fatalf("sources after a failed sync: %d", got)
	}

	// Unsubscribing keeps the sources; with sources=1 they go too.
	if code := c.do("DELETE", path, nil, nil); code != http.StatusNoContent {
		t.Fatalf("unsubscribe: %d", code)
	}
	if got := len(c.sources("")); got != before+3 {
		t.Fatalf("sources after unsubscribing: %d", got)
	}
	set(map[string]any{"name": "丁", "type": "comic", "baseUrl": c.site.URL + "/demo/comic"})
	if code := c.do("POST", "/api/subscriptions", map[string]string{"url": srv.URL + "/other.json", "name": "另一份"}, &res); code != 200 || res.Subscription.Name != "另一份" {
		t.Fatalf("second subscription: %d %+v", code, res)
	}
	if code := c.do("DELETE", fmt.Sprintf("/api/subscriptions/%d?sources=1", res.Subscription.ID), nil, nil); code != http.StatusNoContent {
		t.Fatalf("unsubscribe with sources: %d", code)
	}
	if got := len(c.sources("")); got != before+3 {
		t.Fatalf("sources after unsubscribing with sources: %d", got)
	}
}

func TestASubscriptionThatCannotBeReadIsNotKept(t *testing.T) {
	c := login(t)
	srv, body := listServer(t)
	for name, in := range map[string]string{
		"no scheme":     "example.invalid/list.json",
		"other scheme":  "file:///etc/passwd",
		"missing file":  srv.URL + "/none.json",
		"too large":     srv.URL + "/big.json",
		"nothing there": "http://127.0.0.1:1/list.json",
	} {
		if code := c.do("POST", "/api/subscriptions", map[string]string{"url": in}, nil); code != http.StatusBadRequest {
			t.Errorf("%s: %d", name, code)
		}
	}
	for name, text := range map[string]string{
		"a page":          "<!DOCTYPE html><html></html>",
		"broken JSON":     `[{"name": "甲"`,
		"no valid source": `[{"bookSourceName": "x"}]`,
		"empty list":      `[]`,
	} {
		body.Store(&text)
		if code := c.do("POST", "/api/subscriptions", map[string]string{"url": srv.URL + "/list.json"}, nil); code != http.StatusBadRequest {
			t.Errorf("%s: %d", name, code)
		}
	}
	var subs []any
	if c.do("GET", "/api/subscriptions", nil, &subs); len(subs) != 0 {
		t.Fatalf("subscriptions kept: %+v", subs)
	}
	if code := c.do("POST", "/api/subscriptions/999/sync", nil, nil); code != http.StatusNotFound {
		t.Fatalf("sync of nothing: %d", code)
	}
}

func TestAListOnThisMachineIsNotRead(t *testing.T) {
	api.SetLocalLists(false)
	defer api.SetLocalLists(true)
	c := login(t)
	srv, body := listServer(t)
	list := `[{"name":"甲","type":"novel","baseUrl":"https://example.com"}]`
	body.Store(&list)
	if code := c.do("POST", "/api/subscriptions", map[string]string{"url": srv.URL + "/list.json"}, nil); code != 400 {
		t.Fatalf("a list on this machine: %d, want 400", code)
	}
	var subs []struct{ ID int64 }
	c.do("GET", "/api/subscriptions", nil, &subs)
	if len(subs) != 0 {
		t.Fatalf("subscriptions = %+v, want none", subs)
	}
}

func TestAListWithAByteOrderMarkIsRead(t *testing.T) {
	c := login(t)
	srv, body := listServer(t)
	list := "\xef\xbb\xbf" + `[{"name":"甲","type":"novel","baseUrl":"https://example.com"}]`
	body.Store(&list)
	var out subscriptionResult
	if code := c.do("POST", "/api/subscriptions", map[string]string{"url": srv.URL + "/list.json"}, &out); code != 200 || out.Added != 1 {
		t.Fatalf("code %d, result %+v", code, out)
	}
}

func TestDeletingASubscriptionThatDoesNotExistDeletesNothing(t *testing.T) {
	c := login(t)
	if code := c.do("POST", "/api/sources", map[string]string{"name": "甲", "type": "novel", "baseUrl": "https://example.com"}, nil); code != 200 {
		t.Fatalf("create: %d", code)
	}
	for _, id := range []string{"0", "99"} {
		if code := c.do("DELETE", "/api/subscriptions/"+id+"?sources=1", nil, nil); code != 404 {
			t.Errorf("delete subscription %s: %d, want 404", id, code)
		}
	}
	var sources []sourceInfo
	c.do("GET", "/api/sources", nil, &sources)
	if !slices.ContainsFunc(sources, func(s sourceInfo) bool { return s.Name == "甲" }) || len(sources) != 3 {
		t.Fatalf("%d sources left, want the two of the demo and the one that was added", len(sources))
	}
}
