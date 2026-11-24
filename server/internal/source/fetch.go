package source

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
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
	acceptPage   = "text/html,application/xhtml+xml,application/xml;q=0.9,application/json;q=0.9,*/*;q=0.8"
	acceptImage  = "image/avif,image/webp,image/apng,image/*,*/*;q=0.8"
)

// RetryDelays are the pauses before each retry of a request that failed temporarily
// (timeout, connection error, 429, 5xx).
var RetryDelays = []time.Duration{500 * time.Millisecond, 1500 * time.Millisecond}

// MaxRetryAfter caps how long a Retry-After header can make one retry wait.
var MaxRetryAfter = 10 * time.Second

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
	// Sites hand out session cookies on the first page and expect them on the next ones.
	jar, _ := cookiejar.New(nil)
	return &Fetcher{
		client: &http.Client{Timeout: 20 * time.Second, Jar: jar},
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

// do sends a request, retrying temporary failures, and returns the body, its content type and
// the final URL after redirects.
func (f *Fetcher) do(ctx context.Context, src *Source, method, url, body, accept string, limit int64) ([]byte, string, string, error) {
	for attempt := 0; ; attempt++ {
		data, ctype, final, err := f.once(ctx, src, method, url, body, accept, limit)
		var fe *FetchError
		if err == nil || attempt == len(RetryDelays) || !errors.As(err, &fe) || !fe.Temporary() {
			return data, ctype, final, err
		}
		// Wait as long as the site asks to, within reason; retrying sooner would fail again.
		wait := RetryDelays[attempt]
		if fe.RetryAfter > wait {
			wait = min(fe.RetryAfter, MaxRetryAfter)
		}
		select {
		case <-time.After(wait):
		case <-ctx.Done():
			return nil, "", "", err
		}
	}
}

func (f *Fetcher) once(ctx context.Context, src *Source, method, url, body, accept string, limit int64) ([]byte, string, string, error) {
	if err := f.throttle(ctx, src); err != nil {
		return nil, "", "", transportError(err)
	}
	if method == "" {
		method = http.MethodGet
	}
	req, err := http.NewRequestWithContext(ctx, strings.ToUpper(method), url, strings.NewReader(body))
	if err != nil {
		return nil, "", "", err
	}
	// Look like a browser: many sites answer 403 to requests without these.
	req.Header.Set("User-Agent", defaultUA)
	req.Header.Set("Accept", accept)
	req.Header.Set("Accept-Language", "zh-CN,zh;q=0.9,en;q=0.8")
	req.Header.Set("Referer", src.BaseURL+"/")
	if body != "" {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	for k, v := range src.Headers {
		req.Header.Set(k, v)
	}
	resp, err := f.client.Do(req)
	if err != nil {
		return nil, "", "", transportError(err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit))
	if err != nil {
		return nil, "", "", transportError(err)
	}
	challenged := resp.Header.Get("Cf-Mitigated") == "challenge" || isChallenge(data)
	if resp.StatusCode >= 400 || challenged {
		return nil, "", "", statusError(resp.StatusCode, challenged, resp.Header.Get("Retry-After"))
	}
	return data, resp.Header.Get("Content-Type"), resp.Request.URL.String(), nil
}

// isChallenge recognises the interstitial page of an anti-bot service, which may come with
// status 200 and would otherwise be parsed as an empty result. It goes by the page title only:
// ordinary pages of protected sites also load scripts from the challenge paths.
func isChallenge(body []byte) bool {
	return len(body) < 64<<10 && bytes.Contains(body, []byte("<title>Just a moment...</title>"))
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

	data, ctype, final, err := f.do(ctx, src, method, url, body, acceptPage, maxPageBytes)
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
	data, _, _, err := f.do(ctx, src, http.MethodGet, url, "", acceptImage, maxImgBytes)
	return data, err
}
