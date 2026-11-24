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
	"regexp"
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
	// devCode is what a client outside WeChat (the H5 build) sends instead of a wx.login code.
	devCode = "h5-dev"
	userKey = "userID"
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

// Login exchanges a wx.login code for a session. appID is the AppID the mini program runs
// under, when the client reports it.
func (a *Auth) Login(ctx context.Context, code, appID string) (*Session, error) {
	openid := devOpenID
	if !a.cfg.DevLogin {
		var err error
		if openid, err = a.wxOpenID(ctx, code, appID); err != nil {
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

// wxErrors are the code2session refusals worth explaining, by errcode.
var wxErrors = map[int]struct {
	status int
	msg    string
}{
	-1:    {http.StatusBadGateway, "微信服务繁忙，请稍后再试"},
	40013: {http.StatusInternalServerError, "服务端配置的 WX_APPID 不正确"},
	40125: {http.StatusInternalServerError, "服务端配置的 WX_SECRET 不正确"},
	// A code only works for the AppID it was issued to, once, within five minutes.
	40029: {http.StatusBadRequest, "登录凭证无效：请确认小程序使用的 AppID 与服务端一致，然后重试"},
	40163: {http.StatusBadRequest, "登录凭证已被使用，请重试"},
	40226: {http.StatusForbidden, "该微信账号被微信判定为高风险，无法登录"},
	45011: {http.StatusTooManyRequests, "登录太频繁，请一分钟后再试"},
}

// plainID matches what an AppID may look like.
var plainID = regexp.MustCompile(`^[0-9A-Za-z_-]{1,32}$`)

// wxOpenID checks what the client sent and asks WeChat whose code it is.
// Its errors are worded for the user; what WeChat answered is their internal error.
func (a *Auth) wxOpenID(ctx context.Context, code, appID string) (string, error) {
	switch {
	case a.cfg.WXAppID == "" || a.cfg.WXSecret == "":
		return "", echo.NewHTTPError(http.StatusInternalServerError, "服务端未配置 WX_APPID / WX_SECRET")
	case code == devCode:
		return "", echo.NewHTTPError(http.StatusBadRequest, "当前不在微信小程序内，无法微信登录（H5 调试版只能连接开启 DEV_LOGIN 的后端）")
	case appID != "" && appID != a.cfg.WXAppID:
		// Anyone can send anything here, and the message is also logged.
		if !plainID.MatchString(appID) {
			appID = "无法识别"
		}
		return "", echo.NewHTTPError(http.StatusBadRequest,
			fmt.Sprintf("小程序使用的 AppID（%s）与服务端配置的（%s）不一致", appID, a.cfg.WXAppID))
	}
	openid, err := a.code2Session(ctx, code)
	var refused *wxError
	if errors.As(err, &refused) && refused.code == -1 {
		// "System busy": WeChat asks to try again shortly.
		time.Sleep(500 * time.Millisecond)
		openid, err = a.code2Session(ctx, code)
	}
	if errors.As(err, &refused) {
		known, ok := wxErrors[refused.code]
		if !ok {
			known.status, known.msg = http.StatusBadGateway, "微信登录失败"
		}
		return "", echo.NewHTTPError(known.status, fmt.Sprintf("%s（微信错误码 %d）", known.msg, refused.code)).SetInternal(refused)
	}
	if err != nil {
		return "", echo.NewHTTPError(http.StatusBadGateway, err.Error())
	}
	return openid, nil
}

// wxError is a refusal from WeChat's code2session.
type wxError struct {
	code int
	msg  string
}

func (e *wxError) Error() string { return fmt.Sprintf("code2session: %d %s", e.code, e.msg) }

func (a *Auth) code2Session(ctx context.Context, code string) (string, error) {
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
		return "", fmt.Errorf("微信服务器的回复无法识别：%w", err)
	}
	if out.OpenID == "" {
		return "", &wxError{out.ErrCode, out.ErrMsg}
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
