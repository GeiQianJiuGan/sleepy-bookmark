package api

import (
	"bytes"
	"encoding/json"
	"io"
	"maps"
	"net/http"
	"slices"
	"strconv"

	"github.com/labstack/echo/v4"

	"github.com/GeiQianJiuGan/sleepy-bookmark/server/internal/library"
	"github.com/GeiQianJiuGan/sleepy-bookmark/server/internal/source"
	"github.com/GeiQianJiuGan/sleepy-bookmark/server/internal/store"
)

type sourceView struct {
	*store.SourceRow
	Rule json.RawMessage `json:"rule"`
	// Auto tells that the source has no rules of its own: its pages are recognised.
	Auto bool `json:"auto"`
	// Health is null while the source works.
	Health *library.Health `json:"health"`
}

func (s *Server) listSources(c echo.Context) error {
	rows, err := s.db.Sources(c.QueryParam("type"), false)
	if err != nil {
		return err
	}
	out := make([]sourceView, len(rows))
	for i, r := range rows {
		out[i] = sourceView{r, json.RawMessage(r.Rule), false, s.lib.Health(r.ID)}
		if rule, err := source.ParseSource([]byte(r.Rule)); err == nil {
			out[i].Auto = rule.IsAuto()
		}
	}
	return c.JSON(http.StatusOK, out)
}

// maxRules bounds a rule document, pasted or fetched.
const maxRules = 2 << 20

// splitRules takes a rule document apart: one rule object or an array of them.
func splitRules(body []byte) ([]json.RawMessage, error) {
	body = bytes.TrimSpace(bytes.TrimPrefix(body, []byte("\xef\xbb\xbf")))
	if len(body) == 0 || body[0] != '[' {
		return []json.RawMessage{body}, nil
	}
	var raws []json.RawMessage
	if err := json.Unmarshal(body, &raws); err != nil {
		return nil, badRequest("JSON 格式错误：" + err.Error())
	}
	return raws, nil
}

// readRules accepts one rule object or an array of them.
func readRules(c echo.Context) ([]*source.Source, error) {
	body, err := io.ReadAll(io.LimitReader(c.Request().Body, maxRules))
	if err != nil {
		return nil, err
	}
	raws, err := splitRules(body)
	if err != nil {
		return nil, err
	}
	out := make([]*source.Source, 0, len(raws))
	for _, raw := range raws {
		src, err := source.ParseSource(raw)
		if err != nil {
			return nil, badRequest("书源规则无效：" + err.Error())
		}
		out = append(out, src)
	}
	return out, nil
}

func nameKey(typ, name string) string { return typ + "\x00" + name }

// createSources imports rules. A rule named like exactly one existing source of the same type
// replaces that source's rule, so that re-importing a newer list keeps the shelf and the history.
func (s *Server) createSources(c echo.Context) error {
	rules, err := readRules(c)
	if err != nil {
		return err
	}
	// Two sources of one name could never be updated by a later import.
	seen := map[string]bool{}
	for _, src := range rules {
		if seen[nameKey(src.Type, src.Name)] {
			return badRequest("导入的列表里有重名的书源：" + src.Name)
		}
		seen[nameKey(src.Type, src.Name)] = true
	}
	ids, updated, err := s.importRules(rules, 0)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, echo.Map{"ids": ids, "updated": updated})
}

// importRules stores rules whose names differ from each other, as sources of the subscription
// if one is given, and tells how many of them replaced the rule of an existing source.
func (s *Server) importRules(rules []*source.Source, subscription int64) (ids []int64, updated int, err error) {
	existing, err := s.db.Sources("", false)
	if err != nil {
		return nil, 0, err
	}
	byName := map[string][]*store.SourceRow{}
	for _, row := range existing {
		byName[nameKey(row.Type, row.Name)] = append(byName[nameKey(row.Type, row.Name)], row)
	}
	ids = make([]int64, 0, len(rules))
	for _, src := range rules {
		var id int64
		same := byName[nameKey(src.Type, src.Name)]
		// Of several sources of the name, the one this list brought earlier is the one it
		// updates; without that, every sync would add one more.
		if mine := slices.IndexFunc(same, func(r *store.SourceRow) bool { return r.SubscriptionID == subscription }); subscription != 0 && mine >= 0 {
			same = same[mine : mine+1]
		}
		if len(same) == 1 {
			id = same[0].ID
			// The source is the list's before it has the list's rule, not after: a rule that
			// someone else wrote is never read as one written here.
			if subscription != 0 {
				if err := s.db.SetSourceSubscription(id, subscription); err != nil {
					return ids, updated, err
				}
			}
			if err := s.replaceRule(same[0], src); err != nil {
				return ids, updated, err
			}
			updated++
		} else {
			raw, _ := json.Marshal(src)
			if id, err = s.db.CreateSource(src.Name, src.Type, string(raw), subscription); err != nil {
				return ids, updated, err
			}
		}
		ids = append(ids, id)
	}
	return ids, updated, nil
}

func (s *Server) updateSource(c echo.Context) error {
	id, err := pathID(c)
	if err != nil {
		return err
	}
	rules, err := readRules(c)
	if err != nil {
		return err
	}
	if len(rules) != 1 {
		return badRequest("一次只能更新一个书源")
	}
	row, err := s.db.Source(id)
	if err != nil {
		return err
	}
	if err := s.replaceRule(row, rules[0]); err != nil {
		return err
	}
	return c.NoContent(http.StatusNoContent)
}

// replaceRule stores a new rule for an existing source and drops what the old rule fetched.
func (s *Server) replaceRule(row *store.SourceRow, rule *source.Source) error {
	id := row.ID
	raw, _ := json.Marshal(rule)
	if err := s.db.UpdateSource(id, rule.Name, rule.Type, string(raw)); err != nil {
		return err
	}
	s.lib.ResetHealth(id)
	// What was fetched with the old rule must not outlive it.
	old, _ := source.ParseSource([]byte(row.Rule))
	if catalog, content := rulesChanged(old, rule); catalog {
		if err := s.db.ResetSourceBooks(id, content); err != nil {
			return err
		}
		if content {
			bookIDs, err := s.db.BookIDsBySource(id)
			if err != nil {
				return err
			}
			for _, bookID := range bookIDs {
				s.disk.RemoveBook(bookID)
			}
		}
	}
	return nil
}

// rulesChanged reports whether an edit affects the stored catalogs of a source's books, and
// whether it also affects the cached chapter content.
func rulesChanged(old, cur *source.Source) (catalog, content bool) {
	if old == nil {
		return true, true
	}
	// baseUrl counts as a content change: it is the default Referer of every request.
	content = old.Type != cur.Type || old.BaseURL != cur.BaseURL || old.Charset != cur.Charset || !maps.Equal(old.Headers, cur.Headers) ||
		old.Content.Body != cur.Content.Body || old.Content.Images != cur.Content.Images ||
		old.Content.NextPage != cur.Content.NextPage || !slices.Equal(old.Content.Purify, cur.Content.Purify)
	catalog = content || old.Detail != cur.Detail || old.Toc != cur.Toc
	return catalog, content
}

func (s *Server) toggleSource(c echo.Context) error {
	id, err := pathID(c)
	if err != nil {
		return err
	}
	var in struct {
		Enabled bool `json:"enabled"`
	}
	if err := c.Bind(&in); err != nil {
		return badRequest("请求格式错误")
	}
	if err := s.db.SetSourceEnabled(id, in.Enabled); err != nil {
		return err
	}
	s.lib.ResetHealth(id)
	return c.NoContent(http.StatusNoContent)
}

// deleteSource also removes the source's books, their favorites / history and cached chapters.
func (s *Server) deleteSource(c echo.Context) error {
	id, err := pathID(c)
	if err != nil {
		return err
	}
	if err := s.removeSource(id); err != nil {
		return err
	}
	return c.NoContent(http.StatusNoContent)
}

func (s *Server) removeSource(id int64) error {
	bookIDs, err := s.db.BookIDsBySource(id)
	if err != nil {
		return err
	}
	if err := s.db.DeleteSource(id); err != nil {
		return err
	}
	for _, bookID := range bookIDs {
		s.disk.RemoveBook(bookID)
	}
	// The saved pages of its comics are of no use any more.
	s.lib.ReleaseKept()
	return nil
}

func (s *Server) testSource(c echo.Context) error {
	rules, err := readRules(c)
	if err != nil {
		return err
	}
	if len(rules) != 1 {
		return badRequest("一次只能测试一个书源")
	}
	// Without a keyword, the book to try is taken from the source's list of recommendations.
	key := c.QueryParam("q")
	if key == "" && rules[0].Recommend == nil {
		return badRequest("缺少搜索关键词 q")
	}
	// A rule tried out for a source of a subscribed list is read as that source is.
	if id := c.QueryParam("id"); id != "" {
		n, err := strconv.ParseInt(id, 10, 64)
		if err != nil {
			return badRequest("无效的 id")
		}
		row, err := s.db.Source(n)
		if err != nil {
			return err
		}
		rules[0].Public = row.Listed
	}
	return c.JSON(http.StatusOK, s.lib.TestSource(c.Request().Context(), rules[0], key))
}
