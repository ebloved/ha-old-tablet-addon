package main

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"oldtabletdash/internal/config"
	"oldtabletdash/internal/ha"
	"oldtabletdash/internal/rss"
	"oldtabletdash/internal/web"
)

type options struct {
	RSSRefresh int    `json:"rss_refresh"`
	LogLevel   string `json:"log_level"`
}

func main() {
	opts := loadOptions()
	log := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: level(opts.LogLevel)}))
	slog.SetDefault(log)

	dataDir := os.Getenv("DASH_DATA")
	if dataDir == "" {
		if st, err := os.Stat("/data"); err == nil && st.IsDir() {
			dataDir = "/data"
		} else {
			dataDir = "./data"
		}
	}

	store, err := config.NewStore(dataDir)
	if err != nil {
		log.Error("cannot load config", "err", err)
		os.Exit(1)
	}

	base := os.Getenv("HA_URL")
	token := os.Getenv("HA_TOKEN")
	if t := os.Getenv("SUPERVISOR_TOKEN"); t != "" {
		base = "http://supervisor/core"
		token = t
	}
	if base == "" {
		base = "http://supervisor/core"
	}

	hac := ha.New(base, token, log)
	feeds := rss.New(time.Duration(opts.RSSRefresh)*time.Second, log)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go hac.Run(ctx)

	ingressAddr := env("INGRESS_ADDR", ":8080")
	tabletAddr := env("TABLET_ADDR", ":8088")

	ingress := &http.Server{
		Addr:              ingressAddr,
		Handler:           web.New(store, hac, feeds, log).Handler(true),
		ReadHeaderTimeout: 10 * time.Second,
	}
	tablet := &http.Server{
		Addr:              tabletAddr,
		Handler:           web.New(store, hac, feeds, log).Handler(false),
		ReadHeaderTimeout: 10 * time.Second,
	}

	errCh := make(chan error, 2)
	go func() {
		log.Info("ingress listening", "addr", ingressAddr)
		errCh <- ingress.ListenAndServe()
	}()
	go func() {
		log.Info("tablet listening", "addr", tabletAddr)
		errCh <- tablet.ListenAndServe()
	}()

	go func() {
		if err := <-errCh; err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("server failed", "err", err)
			stop()
		}
	}()

	<-ctx.Done()
	log.Info("shutting down")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = ingress.Shutdown(shutdownCtx)
	_ = tablet.Shutdown(shutdownCtx)
}

func loadOptions() options {
	o := options{RSSRefresh: 600, LogLevel: "info"}
	b, err := os.ReadFile("/data/options.json")
	if err == nil {
		_ = json.Unmarshal(b, &o)
	}
	if o.RSSRefresh <= 0 {
		o.RSSRefresh = 600
	}
	return o
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func level(s string) slog.Level {
	switch s {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
