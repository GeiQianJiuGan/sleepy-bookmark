// Package auth handles WeChat mini program login and JWT sessions.
package auth

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/labstack/echo/v4"

	"github.com/GeiQianJiuGan/sleepy-bookmark/server/internal/config"
	"github.com/GeiQianJiuGan/sleepy-bookmark/server/internal/store"
)

const (
	tokenTTL  = 30 * 24 * time.Hour
	devOpenID = "dev"
	userKey   = "userID"
)

var ErrForbidden = errors.New("该微信账号不在白名单内")

type Auth struct {
	cfg    config.Config
	db     *store.DB
	secret []byte
}

// New loads the JWT secret from config, or creates and persists a random one in the data dir.
func New(cfg config.Config, db *store.DB) (*Auth, error) {
	secret := []byte(cfg.JWTSecret)
	if len(secret) == 0 {
		path := filepath.Join(cfg.DataDir, "jwt.secret")
		var err error
		if secret, err = os.ReadFile(path); err != nil {
			secret = make([]byte, 32)
			rand.Read(secret)
			if err := os.WriteFile(path, secret, 0o600); err != nil {
				return nil, err
			}
		}
	}
	return &Auth{cfg, db, secret}, nil
}

// Login exchanges a wx.login code for a session token.
func (a *Auth) Login(ctx context.Context, code string) (string, error) {
	openid := devOpenID
	if !a.cfg.DevLogin {
		var err error
		if openid, err = a.code2Session(ctx, code); err != nil {
			return "", err
		}
	}
	user, err := a.db.UserByOpenID(openid)
	if errors.Is(err, store.ErrNotFound) {
		if !a.mayRegister(openid) {
			return "", fmt.Errorf("%w（openid: %s）", ErrForbidden, openid)
		}
		user, err = a.db.CreateUser(openid)
	}
	if err != nil {
		return "", err
	}
	if len(a.cfg.OpenIDWhitelist) > 0 && !a.cfg.DevLogin && !slices.Contains(a.cfg.OpenIDWhitelist, openid) {
		return "", fmt.Errorf("%w（openid: %s）", ErrForbidden, openid)
	}
	return jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.RegisteredClaims{
		Subject:   strconv.FormatInt(user.ID, 10),
		ExpiresAt: jwt.NewNumericDate(time.Now().Add(tokenTTL)),
	}).SignedString(a.secret)
}

// mayRegister: with a whitelist only listed openids may join; without one only the very first user may.
func (a *Auth) mayRegister(openid string) bool {
	if a.cfg.DevLogin {
		return true
	}
	if len(a.cfg.OpenIDWhitelist) > 0 {
		return slices.Contains(a.cfg.OpenIDWhitelist, openid)
	}
	n, err := a.db.UserCount()
	return err == nil && n == 0
}

var wxSessionURL = "https://api.weixin.qq.com/sns/jscode2session"

func (a *Auth) code2Session(ctx context.Context, code string) (string, error) {
	if a.cfg.WXAppID == "" || a.cfg.WXSecret == "" {
		return "", errors.New("服务端未配置 WX_APPID / WX_SECRET")
	}
	q := url.Values{"appid": {a.cfg.WXAppID}, "secret": {a.cfg.WXSecret}, "js_code": {code}, "grant_type": {"authorization_code"}}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, wxSessionURL+"?"+q.Encode(), nil)
	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		// A *url.Error prints the request URL, which contains the app secret.
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err
		}
		return "", fmt.Errorf("无法连接微信服务器：%w", err)
	}
	defer resp.Body.Close()
	var out struct {
		OpenID  string `json:"openid"`
		ErrCode int    `json:"errcode"`
		ErrMsg  string `json:"errmsg"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", err
	}
	if out.OpenID == "" {
		return "", fmt.Errorf("微信登录失败：%d %s", out.ErrCode, out.ErrMsg)
	}
	return out.OpenID, nil
}

// Middleware requires a valid "Authorization: Bearer <token>" header.
func (a *Auth) Middleware(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c echo.Context) error {
		raw := strings.TrimPrefix(c.Request().Header.Get("Authorization"), "Bearer ")
		var claims jwt.RegisteredClaims
		_, err := jwt.ParseWithClaims(raw, &claims, func(*jwt.Token) (any, error) { return a.secret, nil },
			jwt.WithValidMethods([]string{"HS256"}))
		if err != nil {
			return echo.NewHTTPError(http.StatusUnauthorized, "未登录或登录已过期")
		}
		id, err := strconv.ParseInt(claims.Subject, 10, 64)
		if err != nil {
			return echo.NewHTTPError(http.StatusUnauthorized, "无效的登录凭证")
		}
		c.Set(userKey, id)
		return next(c)
	}
}

func UserID(c echo.Context) int64 {
	id, _ := c.Get(userKey).(int64)
	return id
}

// Sign authorizes one proxied image URL. <image> tags cannot send an Authorization header,
// so the image endpoint accepts this signature instead of a token.
func (a *Auth) Sign(parts ...string) string {
	mac := hmac.New(sha256.New, a.secret)
	mac.Write([]byte(strings.Join(parts, "\n")))
	return hex.EncodeToString(mac.Sum(nil))[:32]
}

func (a *Auth) Verify(sig string, parts ...string) bool {
	return hmac.Equal([]byte(sig), []byte(a.Sign(parts...)))
}
