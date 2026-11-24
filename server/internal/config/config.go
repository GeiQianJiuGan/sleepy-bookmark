package config

import (
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Addr            string
	DataDir         string
	JWTSecret       string
	WXAppID         string
	WXSecret        string
	OpenIDWhitelist []string
	// DevLogin skips WeChat code2session and logs every client in as one fixed user.
	DevLogin bool
	// DemoSource mounts a built-in fake novel/comic site under /demo and seeds two sources for it.
	DemoSource      bool
	PublicURL       string
	PrefetchAhead   int
	RefreshInterval time.Duration
	ImgCacheMaxMB   int64
	// KeptMaxMB bounds the pages of favorited comics that are saved for good.
	KeptMaxMB int64
}

func Load() Config {
	c := Config{
		Addr:            env("ADDR", ":8080"),
		DataDir:         env("DATA_DIR", "./data"),
		JWTSecret:       os.Getenv("JWT_SECRET"),
		WXAppID:         os.Getenv("WX_APPID"),
		WXSecret:        os.Getenv("WX_SECRET"),
		DevLogin:        envBool("DEV_LOGIN"),
		DemoSource:      envBool("DEMO_SOURCE"),
		PublicURL:       strings.TrimRight(os.Getenv("PUBLIC_URL"), "/"),
		PrefetchAhead:   envInt("PREFETCH_AHEAD", 5),
		RefreshInterval: time.Duration(envInt("REFRESH_INTERVAL_MINUTES", 180)) * time.Minute,
		ImgCacheMaxMB:   int64(envInt("IMG_CACHE_MAX_MB", 2048)),
		KeptMaxMB:       int64(envInt("KEPT_MAX_MB", 10240)),
	}
	for _, id := range strings.Split(os.Getenv("OPENID_WHITELIST"), ",") {
		if id = strings.TrimSpace(id); id != "" {
			c.OpenIDWhitelist = append(c.OpenIDWhitelist, id)
		}
	}
	if c.PublicURL == "" {
		port := c.Addr[strings.LastIndex(c.Addr, ":")+1:]
		c.PublicURL = "http://127.0.0.1:" + port
	}
	return c
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
