package api

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"regexp"
	"strings"
	"time"

	"github.com/labstack/echo/v4"

	"github.com/GeiQianJiuGan/sleepy-bookmark/server/internal/source"
	"github.com/GeiQianJiuGan/sleepy-bookmark/server/internal/store"
)

// subscriptionClient reads a list from the public internet only: its address comes from a user,
// and must not be a way to reach services behind the server.
var subscriptionClient = &http.Client{Timeout: 2 * time.Minute, Transport: source.PublicTransport()}

var (
	reGitHubBlob = regexp.MustCompile(`^https://github\.com/([^/]+)/([^/]+)/(?:blob|raw)/(.+)$`)
	reGist       = regexp.MustCompile(`^https://gist\.github\.com/([^/]+)/([0-9a-f]+)/?$`)
	reGiteeBlob  = regexp.MustCompile(`^https://gitee\.com/([^/]+)/([^/]+)/blob/(.+)$`)
	reGitLabBlob = regexp.MustCompile(`^(https://[^/]+/.+?)/-/blob/(.+)$`)
)

// fileURL turns the address of a file's page on GitHub/Gist/Gitee/GitLab into the raw file URL.
func fileURL(u string) string {
	if m := reGitHubBlob.FindStringSubmatch(u); m != nil {
		return "https://raw.githubusercontent.com/" + m[1] + "/" + m[2] + "/" + m[3]
	}
	if m := reGist.FindStringSubmatch(u); m != nil {
		return "https://gist.githubusercontent.com/" + m[1] + "/" + m[2] + "/raw"
	}
	if m := reGiteeBlob.FindStringSubmatch(u); m != nil {
		return "https://gitee.com/" + m[1] + "/" + m[2] + "/raw/" + m[3]
	}
	if m := reGitLabBlob.FindStringSubmatch(u); m != nil {
		return m[1] + "/-/raw/" + m[2]
	}
	return u
}

// skipped is a rule of a subscribed list that was not imported.
type skipped struct {
	Name  string `json:"name"`
	Error string `json:"error"`
}

// ruleList collects the rules of a list one at a time, so that a list of any size costs the
// memory of the rules it keeps and not of the document they came in.
type ruleList struct {
	rules []*source.Source
	left  []skipped
	seen  map[string]bool
	n     int
}

func (l *ruleList) add(raw json.RawMessage) {
	l.n++
	name := fmt.Sprintf("第 %d 个", l.n)
	if len(raw) > maxRule {
		l.left = append(l.left, skipped{name, "单个书源超过 1 MB"})
		return
	}
	src, err := source.ParseSourceFlexible(raw)
	if src != nil && src.Name != "" {
		name = src.Name
	} else {
		var probe struct {
			Name           string `json:"name"`
			BookSourceName string `json:"bookSourceName"`
		}
		if json.Unmarshal(raw, &probe) == nil {
			if probe.Name != "" {
				name = probe.Name
			} else if probe.BookSourceName != "" {
				name = probe.BookSourceName
			}
		}
	}
	if l.seen == nil {
		l.seen = map[string]bool{}
	}
	switch {
	case err != nil:
		l.left = append(l.left, skipped{name, err.Error()})
	case l.seen[nameKey(src.Type, src.Name)]:
		l.left = append(l.left, skipped{name, "列表里有重名的书源"})
	default:
		l.seen[nameKey(src.Type, src.Name)] = true
		l.rules = append(l.rules, src)
	}
}

func parseRuleList(raws []json.RawMessage) ([]*source.Source, []skipped) {
	var l ruleList
	for _, raw := range raws {
		l.add(raw)
	}
	return l.rules, l.left
}

// maxRule bounds one rule of a streamed list; the list itself may be of any size.
const maxRule = 1 << 20

var (
	errNotRules = errors.New("not a rule list")
	errRuleSize = errors.New("rule too large")
)

// metered fails a read once more than left bytes were read since it was last reset: the decoder
// buffers a whole value, and one value must not be a way to make it buffer without end.
type metered struct {
	r    io.Reader
	left int
}

func (m *metered) Read(p []byte) (int, error) {
	if m.left <= 0 {
		return 0, errRuleSize
	}
	n, err := m.r.Read(p[:min(len(p), m.left)])
	m.left -= n
	return n, err
}

// streamRules reads a rule document of any size from r: one rule object, an array of rules, or
// an object with a "sources" array. The rules are parsed as they arrive.
func streamRules(r io.Reader) ([]*source.Source, []skipped, error) {
	br := bufio.NewReader(r)
	if bom, _ := br.Peek(3); string(bom) == "\xef\xbb\xbf" {
		br.Discard(3)
	}
	for {
		c, err := br.ReadByte()
		if err != nil {
			return nil, nil, errNotRules
		}
		if c == ' ' || c == '\t' || c == '\r' || c == '\n' {
			continue
		}
		br.UnreadByte()
		if c != '[' && c != '{' {
			return nil, nil, errNotRules
		}
		break
	}
	// A value that is read whole has room for its own size and what the decoder reads ahead.
	m := &metered{r: br, left: 4 * maxRule}
	dec := json.NewDecoder(m)
	var list ruleList
	// more gives every value the room anew, whatever the one before it used.
	more := func() bool {
		m.left = 4 * maxRule
		ok := dec.More()
		m.left = 4 * maxRule
		return ok
	}
	elements := func() error {
		for more() {
			var raw json.RawMessage
			if err := dec.Decode(&raw); err != nil {
				return err
			}
			list.add(raw)
		}
		_, err := dec.Token()
		return err
	}
	open, err := dec.Token()
	if err != nil {
		return nil, nil, err
	}
	if open == json.Delim('[') {
		if err := elements(); err != nil {
			return nil, nil, err
		}
		return list.rules, list.left, nil
	}
	// An object is a bundle when it has a "sources" array, and one rule otherwise. Which it is
	// shows only at that key, so the other fields are kept until then, up to the size of a rule.
	var fields bytes.Buffer
	bundle := false
	for more() {
		key, err := dec.Token()
		if err != nil {
			return nil, nil, err
		}
		name, _ := key.(string)
		if name == "sources" && !bundle {
			// The token after the key tells an array from anything else, and opens it.
			val, err := dec.Token()
			if err != nil {
				return nil, nil, err
			}
			switch val {
			case json.Delim('['):
				bundle = true
				fields = bytes.Buffer{}
				if err := elements(); err != nil {
					return nil, nil, err
				}
			case json.Delim('{'):
				// No rule has an object there; it is read past.
				for depth := 1; depth > 0; {
					m.left = 4 * maxRule
					t, err := dec.Token()
					if err != nil {
						return nil, nil, err
					}
					if d, ok := t.(json.Delim); ok && (d == '{' || d == '[') {
						depth++
					} else if ok {
						depth--
					}
				}
			}
			continue
		}
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			return nil, nil, err
		}
		if bundle {
			continue
		}
		if fields.Len()+len(raw) > maxRule {
			return nil, nil, errRuleSize
		}
		if fields.Len() > 0 {
			fields.WriteByte(',')
		}
		k, _ := json.Marshal(name)
		fields.Write(k)
		fields.WriteByte(':')
		fields.Write(raw)
	}
	if !bundle {
		list.add(json.RawMessage("{" + fields.String() + "}"))
	}
	return list.rules, list.left, nil
}

func isSyntax(err error) bool {
	var syn *json.SyntaxError
	return errors.As(err, &syn)
}

// fetchRules downloads a subscribed list. Rules that are not valid are left out and reported:
// one bad entry should not keep the rest of a list from being used.
func fetchRules(ctx context.Context, u string) ([]*source.Source, []skipped, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fileURL(u), nil)
	if err != nil {
		return nil, nil, badRequest("订阅地址无效")
	}
	req.Header.Set("Accept", "application/json, text/plain, */*")
	resp, err := subscriptionClient.Do(req)
	if err != nil {
		return nil, nil, badRequest("订阅地址打不开，请检查地址或稍后再试")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, nil, badRequest(fmt.Sprintf("订阅地址返回了 %d，请检查地址（私有仓库的文件读不到）", resp.StatusCode))
	}
	rules, left, err := streamRules(resp.Body)
	switch {
	case errors.Is(err, errNotRules):
		return nil, nil, badRequest("订阅地址返回的不是书源列表（JSON）。GitHub 上的文件请用它的 Raw 地址")
	case errors.Is(err, errRuleSize):
		return nil, nil, badRequest("订阅里有一个书源超过 1 MB，无法读取")
	case isSyntax(err):
		return nil, nil, badRequest("JSON 格式错误：" + err.Error())
	case err != nil:
		return nil, nil, badRequest("订阅内容没有读完，请稍后再试")
	}
	if len(rules) == 0 {
		msg := "订阅里没有可用的书源"
		if len(left) > 0 {
			msg += "：" + left[0].Name + " " + left[0].Error + "。它可能是别的应用的书源格式"
		}
		return nil, left, badRequest(msg)
	}
	return rules, left, nil
}

func (s *Server) listSubscriptions(c echo.Context) error {
	list, err := s.db.Subscriptions()
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, list)
}

// createSubscription imports the list at a URL and remembers the URL, so that the list can be
// synced again. Subscribing to a URL a second time syncs the subscription that exists.
func (s *Server) createSubscription(c echo.Context) error {
	var in struct {
		Name string `json:"name"`
		URL  string `json:"url"`
	}
	if err := c.Bind(&in); err != nil {
		return badRequest("请求格式错误")
	}
	in.URL, in.Name = strings.TrimSpace(in.URL), strings.TrimSpace(in.Name)
	u, err := url.Parse(in.URL)
	if err != nil || u.Scheme != "http" && u.Scheme != "https" || u.Host == "" {
		return badRequest("订阅地址必须以 http:// 或 https:// 开头")
	}
	sub, err := s.db.SubscriptionByURL(in.URL)
	if errors.Is(err, store.ErrNotFound) {
		// A list that cannot be read is not subscribed to: there would be nothing to show for it.
		rules, left, err := fetchRules(c.Request().Context(), in.URL)
		if err != nil {
			return err
		}
		if in.Name == "" {
			in.Name = strings.TrimSuffix(path.Base(u.Path), path.Ext(u.Path))
			if in.Name == "" || in.Name == "." || in.Name == "/" || in.Name == "raw" {
				in.Name = u.Host
			}
		}
		id, err := s.db.CreateSubscription(in.Name, in.URL)
		if err != nil {
			return err
		}
		return s.applySubscription(c, id, rules, left)
	}
	if err != nil {
		return err
	}
	return s.sync(c, sub)
}

func (s *Server) syncSubscription(c echo.Context) error {
	id, err := pathID(c)
	if err != nil {
		return err
	}
	sub, err := s.db.Subscription(id)
	if err != nil {
		return err
	}
	return s.sync(c, sub)
}

func (s *Server) sync(c echo.Context, sub *store.Subscription) error {
	rules, left, err := fetchRules(c.Request().Context(), sub.URL)
	if err != nil {
		var he *echo.HTTPError
		if msg, ok := "", errors.As(err, &he); ok {
			msg, _ = he.Message.(string)
			if err := s.db.SetSubscriptionSynced(sub.ID, msg); err != nil {
				return err
			}
		}
		return err
	}
	return s.applySubscription(c, sub.ID, rules, left)
}

// applySubscription imports a fetched list. Sources that the list no longer names are kept:
// removing them would remove their books from shelves and histories.
func (s *Server) applySubscription(c echo.Context, id int64, rules []*source.Source, left []skipped) error {
	s.lists.Lock()
	defer s.lists.Unlock()
	// The list took a while to fetch, and the subscription may have been deleted since.
	if _, err := s.db.Subscription(id); err != nil {
		return err
	}
	ids, updated, err := s.importRules(rules, id)
	if err != nil {
		return err
	}
	if err := s.db.SetSubscriptionSynced(id, ""); err != nil {
		return err
	}
	sub, err := s.db.Subscription(id)
	if err != nil {
		return err
	}
	if left == nil {
		left = []skipped{}
	}
	return c.JSON(http.StatusOK, echo.Map{"subscription": sub, "added": len(ids) - updated, "updated": updated, "skipped": left})
}

// deleteSubscription keeps the imported sources unless sources=1 asks to delete them as well,
// with their books, favorites and history.
func (s *Server) deleteSubscription(c echo.Context) error {
	id, err := pathID(c)
	if err != nil {
		return err
	}
	s.lists.Lock()
	defer s.lists.Unlock()
	if _, err := s.db.Subscription(id); err != nil {
		return err
	}
	if c.QueryParam("sources") == "1" {
		rows, err := s.db.SourcesBySubscription(id)
		if err != nil {
			return err
		}
		for _, row := range rows {
			if err := s.removeSource(row.ID); err != nil {
				return err
			}
		}
	}
	if err := s.db.DeleteSubscription(id); err != nil {
		return err
	}
	return c.NoContent(http.StatusNoContent)
}

// syncAllSubscriptions syncs every configured subscription in order and reports a combined summary.
func (s *Server) syncAllSubscriptions(c echo.Context) error {
	subs, err := s.db.Subscriptions()
	if err != nil {
		return err
	}
	type subOutcome struct {
		ID      int64     `json:"id"`
		Name    string    `json:"name"`
		Added   int       `json:"added"`
		Updated int       `json:"updated"`
		Skipped []skipped `json:"skipped"`
		Error   string    `json:"error,omitempty"`
	}
	outcomes := make([]subOutcome, 0, len(subs))
	totalAdded, totalUpdated, syncedCount, failedCount := 0, 0, 0, 0
	for _, sub := range subs {
		rules, left, err := fetchRules(c.Request().Context(), sub.URL)
		if err != nil {
			msg := err.Error()
			var he *echo.HTTPError
			if errors.As(err, &he) {
				if m, ok := he.Message.(string); ok {
					msg = m
				}
			}
			_ = s.db.SetSubscriptionSynced(sub.ID, msg)
			failedCount++
			outcomes = append(outcomes, subOutcome{ID: sub.ID, Name: sub.Name, Skipped: []skipped{}, Error: msg})
			continue
		}
		s.lists.Lock()
		if _, err := s.db.Subscription(sub.ID); err != nil {
			s.lists.Unlock()
			continue
		}
		ids, updated, err := s.importRules(rules, sub.ID)
		if err == nil {
			err = s.db.SetSubscriptionSynced(sub.ID, "")
		}
		s.lists.Unlock()
		if err != nil {
			failedCount++
			outcomes = append(outcomes, subOutcome{ID: sub.ID, Name: sub.Name, Skipped: []skipped{}, Error: err.Error()})
			continue
		}
		if left == nil {
			left = []skipped{}
		}
		added := len(ids) - updated
		totalAdded += added
		totalUpdated += updated
		syncedCount++
		outcomes = append(outcomes, subOutcome{ID: sub.ID, Name: sub.Name, Added: added, Updated: updated, Skipped: left})
	}
	return c.JSON(http.StatusOK, echo.Map{
		"synced":  syncedCount,
		"failed":  failedCount,
		"added":   totalAdded,
		"updated": totalUpdated,
		"results": outcomes,
	})
}

// ServeSourcesFeed mounts a public read-only JSON endpoint so an operator can host their own
// subscription list directly from this server via SOURCES_FEED_FILE.
func ServeSourcesFeed(e *echo.Echo, filePath string) {
	e.GET("/api/sources/feed.json", func(c echo.Context) error {
		f, err := os.Open(filePath)
		if err != nil {
			return echo.NewHTTPError(http.StatusNotFound, "书源订阅文件不可用")
		}
		defer f.Close()
		c.Response().Header().Set("Cache-Control", "public, max-age=300")
		return c.Stream(http.StatusOK, "application/json; charset=utf-8", f)
	})
}

// LoadRulesFile reads a local JSON source rule file (single object, array, or {sources:[...]})
// and returns all valid rules along with the number of skipped entries.
func LoadRulesFile(filePath string) ([]*source.Source, int, error) {
	f, err := os.Open(filePath)
	if err != nil {
		return nil, 0, err
	}
	defer f.Close()
	rules, left, err := streamRules(f)
	return rules, len(left), err
}
