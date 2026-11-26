package api

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/labstack/echo/v4"

	"github.com/GeiQianJiuGan/sleepy-bookmark/server/internal/auth"
	"github.com/GeiQianJiuGan/sleepy-bookmark/server/internal/library"
	"github.com/GeiQianJiuGan/sleepy-bookmark/server/internal/store"
)

// sourceParam reads the optional source query parameter; zero means every source.
func sourceParam(c echo.Context) (int64, error) {
	v := c.QueryParam("source")
	if v == "" {
		return 0, nil
	}
	id, err := strconv.ParseInt(v, 10, 64)
	if err != nil || id <= 0 {
		return 0, badRequest("source 不是书源编号")
	}
	return id, nil
}

func (s *Server) search(c echo.Context) error {
	key := strings.TrimSpace(c.QueryParam("q"))
	if key == "" {
		return badRequest("缺少搜索关键词 q")
	}
	page, _ := strconv.Atoi(c.QueryParam("page"))
	opt := library.SearchOptions{Exact: c.QueryParam("match") == "exact"}
	var err error
	if opt.SourceID, err = sourceParam(c); err != nil {
		return err
	}
	books, errs, err := s.lib.Search(c.Request().Context(), c.QueryParam("type"), key, page, opt)
	if err != nil {
		return err
	}
	for _, b := range books {
		b.Cover = s.imgURL(b.SourceID, b.Cover)
	}
	return c.JSON(http.StatusOK, echo.Map{"books": books, "errors": errs})
}

func (s *Server) recommend(c echo.Context) error {
	sourceID, err := sourceParam(c)
	if err != nil {
		return err
	}
	groups, err := s.lib.Recommend(c.Request().Context(), c.QueryParam("type"), sourceID)
	if err != nil {
		return err
	}
	for i, g := range groups {
		books := make([]*store.Book, len(g.Books))
		for i, b := range g.Books {
			books[i] = s.signed(b)
		}
		groups[i].Books = books
	}
	return c.JSON(http.StatusOK, groups)
}

// pageParams reads offset and limit; a page is 20 books unless asked otherwise, 50 at most.
func pageParams(c echo.Context) (offset, limit int) {
	offset, _ = strconv.Atoi(c.QueryParam("offset"))
	limit, _ = strconv.Atoi(c.QueryParam("limit"))
	if limit <= 0 {
		limit = 20
	}
	return max(offset, 0), min(limit, 50)
}

// signed returns a copy of a book with a cover link the client can load. The lists are cached
// and shared between requests, so the book itself is left alone.
func (s *Server) signed(b *store.Book) *store.Book {
	out := *b
	out.Cover = s.imgURL(b.SourceID, b.Cover)
	return &out
}

// rank is one source's list, a page at a time.
func (s *Server) rank(c echo.Context) error {
	sourceID, err := sourceParam(c)
	if err != nil {
		return err
	}
	if sourceID == 0 {
		return badRequest("缺少书源编号 source")
	}
	offset, limit := pageParams(c)
	list, total, err := s.lib.Rank(c.Request().Context(), sourceID, offset, limit)
	if err != nil {
		return err
	}
	books := make([]*store.Book, len(list.Books))
	for i, b := range list.Books {
		books[i] = s.signed(b)
	}
	return c.JSON(http.StatusOK, echo.Map{"sourceId": list.SourceID, "sourceName": list.SourceName, "total": total, "books": books})
}

// feed is the recommendations across all sources, a page at a time.
func (s *Server) feed(c echo.Context) error {
	offset, limit := pageParams(c)
	books, total, err := s.lib.Feed(c.Request().Context(), auth.UserID(c), c.QueryParam("type"), offset, limit)
	if err != nil {
		return err
	}
	out := make([]library.FeedBook, len(books))
	for i, b := range books {
		b.Book = s.signed(b.Book)
		out[i] = b
	}
	return c.JSON(http.StatusOK, echo.Map{"total": total, "books": out})
}

func (s *Server) shelfJSON(c echo.Context, books []*store.ShelfBook, err error) error {
	if err != nil {
		return err
	}
	for _, b := range books {
		b.Cover = s.imgURL(b.SourceID, b.Cover)
	}
	return c.JSON(http.StatusOK, books)
}

func (s *Server) book(c echo.Context) error {
	id, err := pathID(c)
	if err != nil {
		return err
	}
	if _, err := s.lib.Book(c.Request().Context(), id); err != nil {
		return err
	}
	b, err := s.db.ShelfBook(auth.UserID(c), id)
	if err != nil {
		return err
	}
	b.Cover = s.imgURL(b.SourceID, b.Cover)
	return c.JSON(http.StatusOK, b)
}

func (s *Server) chapters(c echo.Context) error {
	id, err := pathID(c)
	if err != nil {
		return err
	}
	refresh, _ := strconv.ParseBool(c.QueryParam("refresh"))
	list, err := s.lib.Chapters(c.Request().Context(), id, refresh)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, list)
}

func (s *Server) chapter(c echo.Context) error {
	id, err := pathID(c)
	if err != nil {
		return err
	}
	ch, err := s.lib.Chapter(c.Request().Context(), id)
	if err != nil {
		return err
	}
	// Concurrent requests for an uncached chapter share one fetch result, so rewrite a copy.
	out := *ch
	out.Origins = ch.Images
	out.Images = make([]string, len(ch.Images))
	for i, img := range ch.Images {
		out.Images[i] = s.imgURL(ch.SourceID, img)
	}
	return c.JSON(http.StatusOK, &out)
}

// alternatives lists the same book in other sources, for switching away from a failing one.
func (s *Server) alternatives(c echo.Context) error {
	id, err := pathID(c)
	if err != nil {
		return err
	}
	books, err := s.lib.Alternatives(c.Request().Context(), id)
	if err != nil {
		return err
	}
	for _, b := range books {
		b.Cover = s.imgURL(b.SourceID, b.Cover)
	}
	return c.JSON(http.StatusOK, books)
}

// switchSource moves the user's shelf entry and reading progress to the book given as "to".
func (s *Server) switchSource(c echo.Context) error {
	id, err := pathID(c)
	if err != nil {
		return err
	}
	var in struct {
		To int64 `json:"to"`
	}
	if err := c.Bind(&in); err != nil || in.To == 0 || in.To == id {
		return badRequest("缺少要换到的书籍 to")
	}
	if err := s.lib.SwitchSource(c.Request().Context(), auth.UserID(c), id, in.To); err != nil {
		return err
	}
	return c.NoContent(http.StatusNoContent)
}

func (s *Server) cacheBook(c echo.Context) error {
	id, err := pathID(c)
	if err != nil {
		return err
	}
	t, err := s.lib.CacheBook(c.Request().Context(), auth.UserID(c), id)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, t)
}

func (s *Server) tasks(c echo.Context) error {
	return c.JSON(http.StatusOK, s.lib.Tasks(auth.UserID(c)))
}

func (s *Server) favorites(c echo.Context) error {
	books, err := s.db.Favorites(auth.UserID(c), c.QueryParam("type"))
	return s.shelfJSON(c, books, err)
}

func (s *Server) addFavorite(c echo.Context) error {
	id, err := pathID(c)
	if err != nil {
		return err
	}
	if err := s.lib.AddFavorite(c.Request().Context(), auth.UserID(c), id); err != nil {
		return err
	}
	return c.NoContent(http.StatusNoContent)
}

func (s *Server) removeFavorite(c echo.Context) error {
	id, err := pathID(c)
	if err != nil {
		return err
	}
	if err := s.lib.RemoveFavorite(auth.UserID(c), id); err != nil {
		return err
	}
	return c.NoContent(http.StatusNoContent)
}

func (s *Server) history(c echo.Context) error {
	books, err := s.db.History(auth.UserID(c), c.QueryParam("type"))
	return s.shelfJSON(c, books, err)
}

func (s *Server) saveProgress(c echo.Context) error {
	var in struct {
		ChapterID int64 `json:"chapterId"`
		Position  int   `json:"position"`
	}
	if err := c.Bind(&in); err != nil {
		return badRequest("请求格式错误")
	}
	ch, err := s.db.Chapter(in.ChapterID)
	if err != nil {
		return err
	}
	if err := s.db.SaveProgress(auth.UserID(c), ch.BookID, ch.ID, in.Position); err != nil {
		return err
	}
	return c.NoContent(http.StatusNoContent)
}

// clearHistory removes one book from the history, or everything when no id is given.
func (s *Server) clearHistory(c echo.Context) error {
	var id int64
	if c.Param("id") != "" {
		var err error
		if id, err = pathID(c); err != nil {
			return err
		}
	}
	if err := s.db.RemoveHistory(auth.UserID(c), id); err != nil {
		return err
	}
	return c.NoContent(http.StatusNoContent)
}
