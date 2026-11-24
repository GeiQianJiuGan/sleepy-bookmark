package api

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
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
)

// fileURL turns the address of a file's page on GitHub into the address of the file itself.
func fileURL(u string) string {
	if m := reGitHubBlob.FindStringSubmatch(u); m != nil {
		return "https://raw.githubusercontent.com/" + m[1] + "/" + m[2] + "/" + m[3]
	}
	if m := reGist.FindStringSubmatch(u); m != nil {
		return "https://gist.githubusercontent.com/" + m[1] + "/" + m[2] + "/raw"
	}
	return u
}

// skipped is a rule of a subscribed list that was not imported.
type skipped struct {
	Name  string `json:"name"`
	Error string `json:"error"`
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
	var rules []*source.Source
	var left []skipped
	seen := map[string]bool{}
	for i, raw := range raws {
		src, err := source.ParseSource(raw)
		name := fmt.Sprintf("第 %d 个", i+1)
		if src != nil && src.Name != "" {
			name = src.Name
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
