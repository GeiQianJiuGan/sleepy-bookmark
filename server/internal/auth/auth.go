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
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/labstack/echo/v4"

	"github.com/GeiQianJiuGan/sleepy-bookmark/server/internal/config"
	"github.com/GeiQianJiuGan/sleepy-bookmark/server/internal/store"
)

const (
	// The access token is short-lived; the refresh token renews it without another wx.login.
	accessTTL  = 2 * time.Hour
	refreshTTL = 30 * 24 * time.Hour
	accessAud  = "access"
	refreshAud = "refresh"
	devOpenID  = "dev"
	userKey    = "userID"
)

var ErrForbidden = errors.New("该微信账号不在白名单内")

type Auth struct {
	cfg    config.Config
	db     *store.DB
	secret []byte
	// loginMu makes "is this the first user?" and the registration one step.
	loginMu sync.Mutex
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
	return &Auth{cfg: cfg, db: db, secret: secret}, nil
}

// Session is what a client holds after logging in.
type Session struct {
	Token        string `json:"token"`
	RefreshToken string `json:"refreshToken"`
	// ExpiresIn is the lifetime of Token in seconds.
	ExpiresIn int `json:"expiresIn"`
}

func (a *Auth) session(userID int64) (*Session, error) {
	sign := func(aud string, ttl time.Duration) (string, error) {
		return jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.RegisteredClaims{
			Subject:   strconv.FormatInt(userID, 10),
			Audience:  jwt.ClaimStrings{aud},
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(ttl)),
		}).SignedString(a.secret)
	}
	token, err := sign(accessAud, accessTTL)
	if err != nil {
		return nil, err
	}
	refresh, err := sign(refreshAud, refreshTTL)
	if err != nil {
		return nil, err
	}
	return &Session{Token: token, RefreshToken: refresh, ExpiresIn: int(accessTTL / time.Second)}, nil
}

// user returns the account a token of the given kind belongs to, if it may still use the server.
func (a *Auth) user(raw, aud string) (int64, bool) {
	var claims jwt.RegisteredClaims
	_, err := jwt.ParseWithClaims(raw, &claims, func(*jwt.Token) (any, error) { return a.secret, nil },
		jwt.WithValidMethods([]string{"HS256"}), jwt.WithAudience(aud), jwt.WithExpirationRequired())
	if err != nil {
		return 0, false
	}
	id, err := strconv.ParseInt(claims.Subject, 10, 64)
	if err != nil {
		return 0, false
	}
	// Checked every time so that removing an openid from the whitelist takes effect at once
	// instead of when the token expires.
	user, err := a.db.User(id)
	if err != nil || !a.whitelisted(user.OpenID) {
		return 0, false
	}
	return id, true
}

// ErrExpired means the client has to log in again.
var ErrExpired = errors.New("登录已过期，请重新登录")

// Refresh exchanges a refresh token for a new session, with a new refresh token.
func (a *Auth) Refresh(refreshToken string) (*Session, error) {
	id, ok := a.user(refreshToken, refreshAud)
	if !ok {
		return nil, ErrExpired
	}
	return a.session(id)
}

// Login exchanges a wx.login code for a session.
func (a *Auth) Login(ctx context.Context, code string) (*Session, error) {
	openid := devOpenID
	if !a.cfg.DevLogin {
		var err error
		if openid, err = a.code2Session(ctx, code); err != nil {
			return nil, err
		}
	}
	a.loginMu.Lock()
	defer a.loginMu.Unlock()
	user, err := a.db.UserByOpenID(openid)
	if errors.Is(err, store.ErrNotFound) {
		if !a.mayRegister(openid) {
			return nil, fmt.Errorf("%w（openid: %s）", ErrForbidden, openid)
		}
		user, err = a.db.CreateUser(openid)
	}
	if err != nil {
		return nil, err
	}
	if !a.whitelisted(openid) {
		return nil, fmt.Errorf("%w（openid: %s）", ErrForbidden, openid)
	}
	return a.session(user.ID)
}

// whitelisted reports whether an existing account may still use the server.
func (a *Auth) whitelisted(openid string) bool {
	return a.cfg.DevLogin || len(a.cfg.OpenIDWhitelist) == 0 || slices.Contains(a.cfg.OpenIDWhitelist, openid)
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

// Middleware requires a valid "Authorization: Bearer <access token>" header.
func (a *Auth) Middleware(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c echo.Context) error {
		raw := strings.TrimPrefix(c.Request().Header.Get("Authorization"), "Bearer ")
		id, ok := a.user(raw, accessAud)
		if !ok {
			return echo.NewHTTPError(http.StatusUnauthorized, "未登录或登录已过期")
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
