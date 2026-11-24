// Package api exposes the HTTP interface used by the mini program.
package api

import (
	"errors"
	"net/http"
	"net/url"
	"strconv"

	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"

	"github.com/GeiQianJiuGan/sleepy-bookmark/server/internal/auth"
	"github.com/GeiQianJiuGan/sleepy-bookmark/server/internal/cache"
	"github.com/GeiQianJiuGan/sleepy-bookmark/server/internal/library"
	"github.com/GeiQianJiuGan/sleepy-bookmark/server/internal/store"
)

type Server struct {
	db   *store.DB
	auth *auth.Auth
	lib  *library.Library
	disk *cache.Disk
}

func New(db *store.DB, a *auth.Auth, lib *library.Library, disk *cache.Disk) *echo.Echo {
	s := &Server{db, a, lib, disk}
	e := echo.New()
	e.HideBanner, e.HidePort = true, true
	e.HTTPErrorHandler = errorHandler
	e.Use(middleware.Recover(), middleware.CORS(), middleware.Gzip())

	api := e.Group("/api")
	api.GET("/health", func(c echo.Context) error { return c.JSON(http.StatusOK, echo.Map{"ok": true}) })
	api.POST("/auth/login", s.login)
	api.GET("/img", s.image)

	g := api.Group("", a.Middleware)
	g.GET("/sources", s.listSources)
	g.POST("/sources", s.createSources)
	g.POST("/sources/test", s.testSource)
	g.PUT("/sources/:id", s.updateSource)
	g.PUT("/sources/:id/enabled", s.toggleSource)
	g.DELETE("/sources/:id", s.deleteSource)

	g.GET("/search", s.search)
	g.GET("/books/:id", s.book)
	g.GET("/books/:id/chapters", s.chapters)
	g.POST("/books/:id/cache", s.cacheBook)
	g.GET("/chapters/:id", s.chapter)
	g.GET("/tasks", s.tasks)

	g.GET("/favorites", s.favorites)
	g.PUT("/favorites/:id", s.addFavorite)
	g.DELETE("/favorites/:id", s.removeFavorite)
	g.GET("/history", s.history)
	g.PUT("/history", s.saveProgress)
	g.DELETE("/history", s.clearHistory)
	g.DELETE("/history/:id", s.clearHistory)
	return e
}

func errorHandler(err error, c echo.Context) {
	if c.Response().Committed {
		return
	}
	code := http.StatusBadGateway // most failures come from a source site
	var he *echo.HTTPError
	switch {
	case errors.As(err, &he):
		code = he.Code
		if msg, ok := he.Message.(string); ok {
			err = errors.New(msg)
		}
	case errors.Is(err, store.ErrNotFound):
		code = http.StatusNotFound
	case errors.Is(err, auth.ErrForbidden):
		code = http.StatusForbidden
	}
	c.JSON(code, echo.Map{"message": err.Error()})
}

func badRequest(msg string) error { return echo.NewHTTPError(http.StatusBadRequest, msg) }

func pathID(c echo.Context) (int64, error) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		return 0, badRequest("无效的 id")
	}
	return id, nil
}

// imgURL rewrites a source image URL to the signed proxy endpoint.
func (s *Server) imgURL(sourceID int64, raw string) string {
	if raw == "" {
		return ""
	}
	sid := strconv.FormatInt(sourceID, 10)
	return "/api/img?s=" + sid + "&u=" + url.QueryEscape(raw) + "&sig=" + s.auth.Sign(sid, raw)
}

func (s *Server) login(c echo.Context) error {
	var in struct {
		Code string `json:"code"`
	}
	if err := c.Bind(&in); err != nil || in.Code == "" {
		return badRequest("缺少 code")
	}
	token, err := s.auth.Login(c.Request().Context(), in.Code)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, echo.Map{"token": token})
}

func (s *Server) image(c echo.Context) error {
	sid, raw := c.QueryParam("s"), c.QueryParam("u")
	if !s.auth.Verify(c.QueryParam("sig"), sid, raw) {
		return echo.NewHTTPError(http.StatusForbidden, "签名无效")
	}
	sourceID, _ := strconv.ParseInt(sid, 10, 64)
	data, err := s.lib.Image(c.Request().Context(), sourceID, raw)
	if err != nil {
		return err
	}
	c.Response().Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	return c.Blob(http.StatusOK, http.DetectContentType(data), data)
}
