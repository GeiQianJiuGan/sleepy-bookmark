package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/labstack/echo/v4"

	"github.com/GeiQianJiuGan/sleepy-bookmark/server/internal/api"
	"github.com/GeiQianJiuGan/sleepy-bookmark/server/internal/auth"
	"github.com/GeiQianJiuGan/sleepy-bookmark/server/internal/cache"
	"github.com/GeiQianJiuGan/sleepy-bookmark/server/internal/config"
	"github.com/GeiQianJiuGan/sleepy-bookmark/server/internal/demo"
	"github.com/GeiQianJiuGan/sleepy-bookmark/server/internal/library"
	"github.com/GeiQianJiuGan/sleepy-bookmark/server/internal/store"
)

func main() {
	cfg := config.Load()
	if err := os.MkdirAll(cfg.DataDir, 0o755); err != nil {
		log.Fatal(err)
	}
	db, err := store.Open(filepath.Join(cfg.DataDir, "sleepy-bookmark.db"))
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()
	a, err := auth.New(cfg, db)
	if err != nil {
		log.Fatal(err)
	}
	disk := cache.New(cfg.DataDir)
	lib := library.New(cfg, db, disk)

	e := api.New(db, a, lib, disk)
	if cfg.DemoSource {
		e.GET("/demo/*", echo.WrapHandler(demo.Handler()))
		for _, src := range demo.Sources(cfg.PublicURL) {
			if err := lib.SeedSource(src); err != nil {
				log.Fatal(err)
			}
		}
		log.Printf("demo sources enabled at %s/demo", cfg.PublicURL)
	}
	if cfg.DevLogin {
		log.Print("WARNING: DEV_LOGIN is on, anyone who can reach this server is logged in")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go lib.Run(ctx)
	go func() {
		log.Printf("listening on %s", cfg.Addr)
		if err := e.Start(cfg.Addr); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatal(err)
		}
	}()
	if cfg.TLSAddr != "" {
		go func() {
			log.Printf("listening on %s (https)", cfg.TLSAddr)
			if err := e.StartTLS(cfg.TLSAddr, cfg.TLSCert, cfg.TLSKey); err != nil && !errors.Is(err, http.ErrServerClosed) {
				log.Fatal(err)
			}
		}()
	}
	<-ctx.Done()
	shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	e.Shutdown(shutdown)
}
