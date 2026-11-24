package api

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/labstack/echo/v4"

	"github.com/GeiQianJiuGan/sleepy-bookmark/server/internal/auth"
	"github.com/GeiQianJiuGan/sleepy-bookmark/server/internal/store"
)

func (s *Server) search(c echo.Context) error {
	key := strings.TrimSpace(c.QueryParam("q"))
	if key == "" {
		return badRequest("缺少搜索关键词 q")
	}
	page, _ := strconv.Atoi(c.QueryParam("page"))
	books, errs, err := s.lib.Search(c.Request().Context(), c.QueryParam("type"), key, page)
	if err != nil {
		return err
	}
	for _, b := range books {
		b.Cover = s.imgURL(b.SourceID, b.Cover)
	}
	return c.JSON(http.StatusOK, echo.Map{"books": books, "errors": errs})
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
	t, err := s.lib.CacheBook(c.Request().Context(), id)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, t)
}

func (s *Server) tasks(c echo.Context) error {
	return c.JSON(http.StatusOK, s.lib.Tasks())
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
	if _, err := s.db.Book(id); err != nil {
		return err
	}
	if err := s.db.AddFavorite(auth.UserID(c), id); err != nil {
		return err
	}
	return c.NoContent(http.StatusNoContent)
}

func (s *Server) removeFavorite(c echo.Context) error {
	id, err := pathID(c)
	if err != nil {
		return err
	}
	if err := s.db.RemoveFavorite(auth.UserID(c), id); err != nil {
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
