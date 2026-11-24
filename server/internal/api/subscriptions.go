package api

import (
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
var subscriptionClient = &http.Client{Timeout: 20 * time.Second, Transport: source.PublicTransport()}

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

func parseRuleList(raws []json.RawMessage) ([]*source.Source, []skipped) {
	var rules []*source.Source
	var left []skipped
	seen := map[string]bool{}
	for i, raw := range raws {
		src, err := source.ParseSourceFlexible(raw)
		name := fmt.Sprintf("第 %d 个", i+1)
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
		switch {
		case err != nil:
			left = append(left, skipped{name, err.Error()})
		case seen[nameKey(src.Type, src.Name)]:
			left = append(left, skipped{name, "列表里有重名的书源"})
		default:
			seen[nameKey(src.Type, src.Name)] = true
			rules = append(rules, src)
		}
	}
	return rules, left
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
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxRules+1))
	if err != nil {
		return nil, nil, badRequest("订阅内容没有读完，请稍后再试")
	}
	if len(body) > maxRules {
		return nil, nil, badRequest("订阅内容超过 2 MB")
	}
	if trimmed := bytes.TrimSpace(bytes.TrimPrefix(body, []byte("\xef\xbb\xbf"))); len(trimmed) == 0 || trimmed[0] != '[' && trimmed[0] != '{' {
		return nil, nil, badRequest("订阅地址返回的不是书源列表（JSON）。GitHub 上的文件请用它的 Raw 地址")
	}
	raws, err := splitRules(body)
	if err != nil {
		return nil, nil, err
	}
	rules, left := parseRuleList(raws)
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
		data, err := os.ReadFile(filePath)
		if err != nil {
			return echo.NewHTTPError(http.StatusNotFound, "书源订阅文件不可用")
		}
		if len(data) > maxRules {
			return echo.NewHTTPError(http.StatusRequestEntityTooLarge, "订阅文件超过 2 MB")
		}
		c.Response().Header().Set("Cache-Control", "public, max-age=300")
		return c.Blob(http.StatusOK, "application/json; charset=utf-8", data)
	})
}

// LoadRulesFile reads a local JSON source rule file (single object, array, or {sources:[...]})
// and returns all valid rules along with the number of skipped entries.
func LoadRulesFile(filePath string) ([]*source.Source, int, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, 0, err
	}
	if len(data) > maxRules {
		return nil, 0, errors.New("书源文件超过 2 MB")
	}
	raws, err := splitRules(data)
	if err != nil {
		return nil, 0, err
	}
	rules, left := parseRuleList(raws)
	return rules, len(left), nil
}
