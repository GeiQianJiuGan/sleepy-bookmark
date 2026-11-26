package api

import (
	"net/http"

	"github.com/labstack/echo/v4"

	"github.com/GeiQianJiuGan/sleepy-bookmark/server/internal/auth"
)

func (s *Server) me(c echo.Context) error {
	userID := auth.UserID(c)
	return c.JSON(http.StatusOK, echo.Map{"id": userID, "admin": s.auth.IsAdmin(userID)})
}

func (s *Server) users(c echo.Context) error {
	users, err := s.db.Users()
	if err != nil {
		return err
	}
	out := make([]userResponse, len(users))
	for i, user := range users {
		out[i] = userResponse{
			ID: user.ID, OpenID: user.OpenID, CreatedAt: user.CreatedAt,
			Admin: s.auth.IsAdmin(user.ID), Favorites: user.Favorites, History: user.History,
		}
	}
	return c.JSON(http.StatusOK, out)
}

type userResponse struct {
	ID        int64  `json:"id"`
	OpenID    string `json:"openid"`
	CreatedAt int64  `json:"createdAt"`
	Admin     bool   `json:"admin"`
	Favorites int    `json:"favorites"`
	History   int    `json:"history"`
}
