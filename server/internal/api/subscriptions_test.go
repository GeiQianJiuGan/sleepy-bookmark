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
	"time"

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

func TestASyncThatOutlivesItsSubscriptionImportsNothing(t *testing.T) {
	c := login(t)
	list := `[{"name":"甲","type":"novel","baseUrl":"https://example.com"},{"name":"乙","type":"novel","baseUrl":"https://example.com"}]`
	asked, release := make(chan struct{}, 1), make(chan struct{})
	var slow atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if slow.Load() {
			asked <- struct{}{}
			<-release
		}
		fmt.Fprint(w, list)
	}))
	defer srv.Close()
	before := len(c.sources(""))
	var res subscriptionResult
	if code := c.do("POST", "/api/subscriptions", map[string]string{"url": srv.URL + "/list.json"}, &res); code != 200 || res.Added != 2 {
		t.Fatalf("subscribe: %d %+v", code, res)
	}
	path := fmt.Sprintf("/api/subscriptions/%d", res.Subscription.ID)

	slow.Store(true)
	synced := make(chan int)
	go func() { synced <- c.do("POST", path+"/sync", nil, nil) }()
	<-asked
	if code := c.do("DELETE", path+"?sources=1", nil, nil); code != http.StatusNoContent {
		t.Fatalf("unsubscribe with sources: %d", code)
	}
	close(release)
	if code := <-synced; code != http.StatusNotFound {
		t.Errorf("sync of a subscription deleted meanwhile: %d, want 404", code)
	}
	if got := len(c.sources("")); got != before {
		t.Errorf("%d sources, want the %d from before the subscription", got, before)
	}
}

func TestSyncingDoesNotAddASourceAgainWhenItsNameIsTaken(t *testing.T) {
	c := login(t)
	srv, body := listServer(t)
	// Two sources of the same name and type were added by hand before the list names one too.
	for _, name := range []string{"甲", "乙"} {
		if code := c.do("POST", "/api/sources", map[string]string{"name": name, "type": "novel", "baseUrl": "https://example.com"}, nil); code != 200 {
			t.Fatalf("create: %d", code)
		}
	}
	all := c.sources("")
	renamed := map[string]string{"name": "甲", "type": "novel", "baseUrl": "https://example.com"}
	if code := c.do("PUT", fmt.Sprintf("/api/sources/%d", all[len(all)-1].ID), renamed, nil); code != http.StatusNoContent {
		t.Fatalf("rename: %d", code)
	}
	list := `[{"name":"甲","type":"novel","baseUrl":"https://example.org"}]`
	body.Store(&list)
	before := len(c.sources(""))
	var res subscriptionResult
	if code := c.do("POST", "/api/subscriptions", map[string]string{"url": srv.URL + "/list.json"}, &res); code != 200 || res.Added != 1 {
		t.Fatalf("subscribe: %d %+v", code, res)
	}
	for range 3 {
		path := fmt.Sprintf("/api/subscriptions/%d/sync", res.Subscription.ID)
		if code := c.do("POST", path, nil, &res); code != 200 || res.Added != 0 || res.Updated != 1 || res.Subscription.Sources != 1 {
			t.Fatalf("sync: %d %+v", code, res)
		}
	}
	if got := len(c.sources("")); got != before+1 {
		t.Fatalf("%d sources, want %d", got, before+1)
	}
}

func TestASourceOfAListKeepsToPublicAddressesOnceUnsubscribed(t *testing.T) {
	c := login(t)
	srv, body := listServer(t)
	rule := map[string]string{"name": "列表带来的", "type": "novel", "baseUrl": c.site.URL + "/demo/novel"}
	raw, _ := json.Marshal(rule)
	list := string(raw)
	body.Store(&list)
	var res subscriptionResult
	if code := c.do("POST", "/api/subscriptions", map[string]string{"url": srv.URL + "/list.json"}, &res); code != 200 || res.Added != 1 {
		t.Fatalf("subscribe: %d %+v", code, res)
	}
	if code := c.do("DELETE", fmt.Sprintf("/api/subscriptions/%d", res.Subscription.ID), nil, nil); code != http.StatusNoContent {
		t.Fatalf("unsubscribe: %d", code)
	}
	all := c.sources("")
	kept := all[len(all)-1]
	if kept.Name != rule["name"] {
		t.Fatalf("the source is gone with its subscription: %+v", all)
	}

	// The site of the tests is on this machine, which a list is not let near.
	api.SetLocalLists(false)
	defer api.SetLocalLists(true)
	var found searchResult
	c.do("GET", fmt.Sprintf("/api/search?type=novel&q=%%E6%%98%%9F%%E6%%B2%%B3&source=%d", kept.ID), nil, &found)
	if len(found.Books) != 0 || len(found.Errors) != 1 || !strings.Contains(found.Errors[0].Error, "公网") {
		t.Fatalf("search of the kept source: %+v", found)
	}
	// An edit of it is tried out under the same terms, and so is a new source of its name, which
	// would be saved over it. A new source of another name is not.
	other := map[string]string{"name": "自己加的", "type": "novel", "baseUrl": rule["baseUrl"]}
	for _, try := range []struct {
		id      string
		rule    map[string]string
		refused bool
	}{{fmt.Sprint(kept.ID), rule, true}, {"", rule, true}, {"", other, false}} {
		var report struct{ FailedStep, Error string }
		if code := c.do("POST", "/api/sources/test?q=%E6%98%9F%E6%B2%B3&id="+try.id, try.rule, &report); code != 200 {
			t.Fatalf("test of %q with id %q: %d", try.rule["name"], try.id, code)
		}
		if strings.Contains(report.Error, "公网") != try.refused || (report.FailedStep != "") != try.refused {
			t.Errorf("test of %q with id %q: %+v, refused should be %v", try.rule["name"], try.id, report, try.refused)
		}
	}
}

// A list is as large as it is: the old 2 MB bound turned ordinary lists away.
func TestASubscriptionLargerThanTwoMegabytesIsImported(t *testing.T) {
	c := login(t)
	srv, body := listServer(t)
	site := c.site.URL + "/demo/novel"
	const n = 600
	var b strings.Builder
	b.WriteString("[")
	for i := range n {
		if i > 0 {
			b.WriteString(",")
		}
		raw, _ := json.Marshal(map[string]any{"name": fmt.Sprint("大", i), "type": "novel", "baseUrl": site, "pad": strings.Repeat("x", 5000)})
		b.Write(raw)
	}
	b.WriteString("]")
	text := b.String()
	if len(text) <= 2<<20 {
		t.Fatalf("list of %d bytes", len(text))
	}
	body.Store(&text)
	var res subscriptionResult
	start := time.Now()
	if code := c.do("POST", "/api/subscriptions", map[string]string{"url": srv.URL + "/lists/big.json"}, &res); code != 200 || res.Added != n {
		t.Fatalf("subscribe: %d, %+v", code, res)
	}
	t.Logf("%d sources in %v", n, time.Since(start))
}
