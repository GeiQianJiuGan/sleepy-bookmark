package config

import (
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Addr string
	// TLSAddr, with TLSCert and TLSKey, adds an HTTPS listener next to the plain one on Addr.
	TLSAddr         string
	TLSCert         string
	TLSKey          string
	DataDir         string
	JWTSecret       string
	WXAppID         string
	WXSecret        string
	OpenIDWhitelist []string
	// OpenRegistration lets anyone with a valid wx.login code register; only people who can open
	// the mini program can get such a code, and the 体验版 opens only for testers added in WeChat.
	OpenRegistration bool
	AdminOpenIDs     []string
	// DevLogin skips WeChat code2session and logs every client in as one fixed user.
	DevLogin bool
	// DemoSource mounts a built-in fake novel/comic site under /demo and seeds two sources for it.
	DemoSource bool
	// SourcesFeedFile exposes a local JSON rule bundle at GET /api/sources/feed.json as a subscription URL.
	SourcesFeedFile string
	// SeedSourcesFile imports rules from a local JSON file on startup (skipping names that already exist).
	SeedSourcesFile string
	PublicURL       string
	PrefetchAhead   int
	RefreshInterval time.Duration
	ImgCacheMaxMB   int64
	// KeptMaxMB bounds the pages of favorited comics that are saved for good.
	KeptMaxMB int64
}

func Load() Config {
	c := Config{
		Addr:             env("ADDR", ":8080"),
		TLSAddr:          os.Getenv("TLS_ADDR"),
		TLSCert:          os.Getenv("TLS_CERT"),
		TLSKey:           os.Getenv("TLS_KEY"),
		DataDir:          env("DATA_DIR", "./data"),
		JWTSecret:        os.Getenv("JWT_SECRET"),
		WXAppID:          os.Getenv("WX_APPID"),
		WXSecret:         os.Getenv("WX_SECRET"),
		OpenRegistration: envBool("OPEN_REGISTRATION"),
		DevLogin:         envBool("DEV_LOGIN"),
		DemoSource:       envBool("DEMO_SOURCE"),
		SourcesFeedFile:  strings.TrimSpace(os.Getenv("SOURCES_FEED_FILE")),
		SeedSourcesFile:  strings.TrimSpace(os.Getenv("SEED_SOURCES_FILE")),
		PublicURL:        strings.TrimRight(os.Getenv("PUBLIC_URL"), "/"),
		PrefetchAhead:    envInt("PREFETCH_AHEAD", 5),
		RefreshInterval:  time.Duration(envInt("REFRESH_INTERVAL_MINUTES", 180)) * time.Minute,
		ImgCacheMaxMB:    int64(envInt("IMG_CACHE_MAX_MB", 2048)),
		KeptMaxMB:        int64(envInt("KEPT_MAX_MB", 10240)),
	}
	c.OpenIDWhitelist = envList("OPENID_WHITELIST")
	c.AdminOpenIDs = envList("ADMIN_OPENIDS")
	if c.PublicURL == "" {
		port := c.Addr[strings.LastIndex(c.Addr, ":")+1:]
		c.PublicURL = "http://127.0.0.1:" + port
	}
	return c
}

func envList(k string) []string {
	var out []string
	for _, id := range strings.Split(os.Getenv(k), ",") {
		if id = strings.TrimSpace(id); id != "" {
			out = append(out, id)
		}
	}
	return out
}

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func envBool(k string) bool {
	v, _ := strconv.ParseBool(os.Getenv(k))
	return v
}

func envInt(k string, def int) int {
	if v, err := strconv.Atoi(os.Getenv(k)); err == nil {
		return v
	}
	return def
}
