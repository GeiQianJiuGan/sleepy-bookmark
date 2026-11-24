package api

import (
	"bytes"
	"encoding/json"
	"io"
	"maps"
	"net/http"
	"slices"

	"github.com/labstack/echo/v4"

	"github.com/GeiQianJiuGan/sleepy-bookmark/server/internal/library"
	"github.com/GeiQianJiuGan/sleepy-bookmark/server/internal/source"
	"github.com/GeiQianJiuGan/sleepy-bookmark/server/internal/store"
)

type sourceView struct {
	*store.SourceRow
	Rule json.RawMessage `json:"rule"`
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
		out[i] = sourceView{r, json.RawMessage(r.Rule), s.lib.Health(r.ID)}
	}
	return c.JSON(http.StatusOK, out)
}

// readRules accepts one rule object or an array of them.
func readRules(c echo.Context) ([]*source.Source, error) {
	body, err := io.ReadAll(io.LimitReader(c.Request().Body, 2<<20))
	if err != nil {
		return nil, err
	}
	var raws []json.RawMessage
	if body = bytes.TrimSpace(body); len(body) > 0 && body[0] == '[' {
		if err := json.Unmarshal(body, &raws); err != nil {
			return nil, badRequest("JSON 格式错误：" + err.Error())
		}
	} else {
		raws = []json.RawMessage{body}
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

// createSources imports rules. A rule named like exactly one existing source of the same type
// replaces that source's rule, so that re-importing a newer list keeps the shelf and the history.
func (s *Server) createSources(c echo.Context) error {
	rules, err := readRules(c)
	if err != nil {
		return err
	}
	existing, err := s.db.Sources("", false)
	if err != nil {
		return err
	}
	byName := map[string][]*store.SourceRow{}
	for _, row := range existing {
		byName[row.Type+"\x00"+row.Name] = append(byName[row.Type+"\x00"+row.Name], row)
	}
	// Two sources of one name could never be updated by a later import.
	seen := map[string]bool{}
	for _, src := range rules {
		if seen[src.Type+"\x00"+src.Name] {
			return badRequest("导入的列表里有重名的书源：" + src.Name)
		}
		seen[src.Type+"\x00"+src.Name] = true
	}
	ids := make([]int64, 0, len(rules))
	updated := 0
	for _, src := range rules {
		if same := byName[src.Type+"\x00"+src.Name]; len(same) == 1 {
			if err := s.replaceRule(same[0], src); err != nil {
				return err
			}
			ids = append(ids, same[0].ID)
			updated++
			continue
		}
		raw, _ := json.Marshal(src)
		id, err := s.db.CreateSource(src.Name, src.Type, string(raw))
		if err != nil {
			return err
		}
		ids = append(ids, id)
	}
	return c.JSON(http.StatusOK, echo.Map{"ids": ids, "updated": updated})
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
	return c.NoContent(http.StatusNoContent)
}

func (s *Server) testSource(c echo.Context) error {
	rules, err := readRules(c)
	if err != nil {
		return err
	}
	if len(rules) != 1 {
		return badRequest("一次只能测试一个书源")
	}
	key := c.QueryParam("q")
	if key == "" {
		return badRequest("缺少搜索关键词 q")
	}
	return c.JSON(http.StatusOK, s.lib.TestSource(c.Request().Context(), rules[0], key))
}
