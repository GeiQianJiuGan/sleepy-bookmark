package source

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/html/charset"
)

const (
	defaultUA    = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0 Safari/537.36"
	maxPageBytes = 8 << 20
	maxImgBytes  = 30 << 20
	pageTTL      = time.Minute
)

type page struct {
	body []byte
	// url is the address the body came from, after redirects.
	url string
	at  time.Time
}

// Fetcher downloads source pages with per-site throttling and a short-lived page cache
// (detail and toc are often the same URL).
type Fetcher struct {
	client *http.Client

	mu    sync.Mutex
	next  map[string]time.Time
	pages map[string]page
}

func NewFetcher() *Fetcher {
	return &Fetcher{
		client: &http.Client{Timeout: 20 * time.Second},
		next:   map[string]time.Time{},
		pages:  map[string]page{},
	}
}

func (f *Fetcher) throttle(ctx context.Context, src *Source) error {
	gap := time.Duration(src.RateLimitMs) * time.Millisecond
	if gap <= 0 {
		gap = 200 * time.Millisecond
	}
	f.mu.Lock()
	now := time.Now()
	at := f.next[src.BaseURL]
	if at.Before(now) {
		at = now
	}
	f.next[src.BaseURL] = at.Add(gap)
	f.mu.Unlock()

	select {
	case <-time.After(time.Until(at)):
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// do returns the body, its content type and the final URL after redirects.
func (f *Fetcher) do(ctx context.Context, src *Source, method, url, body string, limit int64) ([]byte, string, string, error) {
	if err := f.throttle(ctx, src); err != nil {
		return nil, "", "", err
	}
	if method == "" {
		method = http.MethodGet
	}
	req, err := http.NewRequestWithContext(ctx, strings.ToUpper(method), url, strings.NewReader(body))
	if err != nil {
		return nil, "", "", err
	}
	req.Header.Set("User-Agent", defaultUA)
	req.Header.Set("Referer", src.BaseURL+"/")
	if body != "" {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	for k, v := range src.Headers {
		req.Header.Set(k, v)
	}
	resp, err := f.client.Do(req)
	if err != nil {
		return nil, "", "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return nil, "", "", fmt.Errorf("%s 返回 HTTP %d", url, resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit))
	return data, resp.Header.Get("Content-Type"), resp.Request.URL.String(), err
}

// Page fetches a text page and converts it to UTF-8. It also returns the page's final URL
// after redirects, which is what relative links in the page are relative to.
func (f *Fetcher) Page(ctx context.Context, src *Source, method, url, body string) ([]byte, string, error) {
	// The charset and headers are part of the key: they change what the same URL returns,
	// and they are what gets edited between two runs of a source test.
	key := fmt.Sprintf("%s %s %s %s %v", method, url, body, src.Charset, src.Headers)
	f.mu.Lock()
	p, ok := f.pages[key]
	f.mu.Unlock()
	if ok && time.Since(p.at) < pageTTL {
		return p.body, p.url, nil
	}

	data, ctype, final, err := f.do(ctx, src, method, url, body, maxPageBytes)
	if err != nil {
		return nil, "", err
	}
	if src.Charset != "" {
		ctype = "text/html; charset=" + src.Charset
	}
	if !strings.Contains(ctype, "json") {
		if r, err := charset.NewReader(strings.NewReader(string(data)), ctype); err == nil {
			if decoded, err := io.ReadAll(r); err == nil {
				data = decoded
			}
		}
	}

	f.mu.Lock()
	if len(f.pages) > 200 {
		for k, v := range f.pages {
			if time.Since(v.at) > pageTTL {
				delete(f.pages, k)
			}
		}
	}
	f.pages[key] = page{data, final, time.Now()}
	f.mu.Unlock()
	return data, final, nil
}

// Image fetches a binary resource with the source's headers (Referer anti-hotlinking).
func (f *Fetcher) Image(ctx context.Context, src *Source, url string) ([]byte, error) {
	data, _, _, err := f.do(ctx, src, http.MethodGet, url, "", maxImgBytes)
	return data, err
}
