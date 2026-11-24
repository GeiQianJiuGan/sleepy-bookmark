package source_test

import (
	"context"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/GeiQianJiuGan/sleepy-bookmark/server/internal/source"
)

func fastRetries(t *testing.T) {
	t.Helper()
	old := source.RetryDelays
	source.RetryDelays = []time.Duration{time.Millisecond, time.Millisecond}
	t.Cleanup(func() { source.RetryDelays = old })
}

func TestTemporaryFailuresAreRetried(t *testing.T) {
	fastRetries(t)
	var hits atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("/chapter", func(w http.ResponseWriter, r *http.Request) {
		if hits.Add(1) < 3 {
			http.Error(w, "busy", http.StatusServiceUnavailable)
			return
		}
		w.Write([]byte(`<div id="content">finally</div>`))
	})
	e, src := site(t, mux)

	c, err := e.Content(context.Background(), src, src.BaseURL+"/chapter", "")
	if err != nil || c.Text != "finally" || hits.Load() != 3 {
		t.Fatalf("content %q, err %v, %d requests", c.Text, err, hits.Load())
	}
}

func TestFailuresAreClassified(t *testing.T) {
	fastRetries(t)
	var gone atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("/busy", func(w http.ResponseWriter, r *http.Request) { http.Error(w, "", http.StatusBadGateway) })
	mux.HandleFunc("/forbidden", func(w http.ResponseWriter, r *http.Request) { http.Error(w, "", http.StatusForbidden) })
	mux.HandleFunc("/gone", func(w http.ResponseWriter, r *http.Request) {
		gone.Add(1)
		http.NotFound(w, r)
	})
	mux.HandleFunc("/challenge", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<html><head><title>Just a moment...</title></head><body></body></html>`))
	})
	// An ordinary page of a site behind the same service: it only loads the detection script.
	mux.HandleFunc("/protected", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<div id="content">text</div><script src="/cdn-cgi/challenge-platform/scripts/jsd/main.js"></script>`))
	})
	mux.HandleFunc("/empty", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`<p>nothing here</p>`)) })
	e, src := site(t, mux)

	for path, want := range map[string]string{
		"/busy":      source.KindBusy,
		"/forbidden": source.KindBlocked,
		"/gone":      source.KindNotFound,
		"/challenge": source.KindBlocked,
		"/empty":     source.KindRule,
		"/protected": "",
	} {
		_, err := e.Content(context.Background(), src, src.BaseURL+path, "")
		if got := source.Kind(err); got != want {
			t.Errorf("%s: kind %q (%v), want %q", path, got, err, want)
		}
		if err != nil && (len(err.Error()) == 0 || strings.Contains(err.Error(), src.BaseURL)) {
			t.Errorf("%s: message %q should be readable and not contain the URL", path, err)
		}
	}
	if gone.Load() != 1 {
		t.Errorf("a 404 was requested %d times, want 1 (not retried)", gone.Load())
	}

	// Nothing listens here any more.
	dead := *src
	dead.BaseURL = "http://127.0.0.1:1"
	if _, err := e.Content(context.Background(), &dead, dead.BaseURL+"/x", ""); source.Kind(err) != source.KindNetwork || !source.Unreachable(err) {
		t.Errorf("dead site: %v (kind %q)", err, source.Kind(err))
	}
}

func TestRetryWaitsForRetryAfter(t *testing.T) {
	fastRetries(t)
	var ready atomic.Int64
	mux := http.NewServeMux()
	mux.HandleFunc("/chapter", func(w http.ResponseWriter, r *http.Request) {
		if ready.CompareAndSwap(0, time.Now().Add(900*time.Millisecond).UnixNano()) || time.Now().UnixNano() < ready.Load() {
			w.Header().Set("Retry-After", "1")
			http.Error(w, "slow down", http.StatusTooManyRequests)
			return
		}
		w.Write([]byte(`<div id="content">ok</div>`))
	})
	e, src := site(t, mux)

	// With the 1 ms test delays both retries would be over long before the site is ready.
	c, err := e.Content(context.Background(), src, src.BaseURL+"/chapter", "")
	if err != nil || c.Text != "ok" {
		t.Fatalf("content %q, err %v", c.Text, err)
	}
}
