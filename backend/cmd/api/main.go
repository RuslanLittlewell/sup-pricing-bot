package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"
	"github.com/rs/zerolog"

	"github.com/littlewell/price-tracker/internal/config"
	"github.com/littlewell/price-tracker/internal/db"
	"github.com/littlewell/price-tracker/internal/handler"
	"github.com/littlewell/price-tracker/internal/renderer"
	"github.com/littlewell/price-tracker/internal/telegram"
)

func main() {
	log := zerolog.New(zerolog.ConsoleWriter{Out: os.Stdout, TimeFormat: time.RFC3339}).
		With().Timestamp().Logger()

	cfg := config.Load()

	tg := telegram.NewClient(cfg.TelegramToken)

	if botInfo, err := tg.GetMe(); err == nil {
		log.Info().Str("bot", botInfo.FirstName).Int64("id", botInfo.ID).Msg("telegram bot connected")
	} else {
		log.Warn().Err(err).Msg("telegram bot connection failed (set TELEGRAM_BOT_TOKEN)")
	}
	if cfg.TelegramWebhook != "" {
		if err := tg.SetWebhook(cfg.TelegramWebhook); err != nil {
			log.Warn().Err(err).Str("url", cfg.TelegramWebhook).Msg("failed to set telegram webhook")
		} else {
			log.Info().Str("url", cfg.TelegramWebhook).Msg("telegram webhook configured")
		}
	}

	rend, err := renderer.New(cfg.ScraperCookies, cfg.ScraperProxy)
	if err != nil {
		log.Fatal().Err(err).Msg("failed to start headless browser")
	}
	defer rend.Close()

	log.Info().Str("env", cfg.Env).Int("port", cfg.Port).Msg("starting api server")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	pool, err := db.ConnectPool(ctx, cfg.DatabaseURL, log)
	if err != nil {
		log.Fatal().Err(err).Msg("failed to connect to database")
	}
	defer pool.Close()

	if err := db.RunMigrations(ctx, pool, log); err != nil {
		log.Fatal().Err(err).Msg("failed to run migrations")
	}

	r := chi.NewRouter()
	r.Use(chimw.RequestID)
	r.Use(chimw.RealIP)
	r.Use(handler.Logger(log))
	r.Use(chimw.Recoverer)
	r.Use(handler.Cors(cfg.CORSOrigin))
	r.Use(handler.Timeout(30 * time.Second))

	r.Get("/healthz", handler.Healthz)

	r.Route("/api", func(r chi.Router) {
		r.Use(handler.RateLimit())

		// Telegram webhook (no auth required)
		r.Post("/telegram/webhook", handler.TelegramWebhook(pool, cfg, tg, log, rend))

		// Extraction endpoints (no auth required for preview)
		r.Post("/extract/preview", handler.ExtractPreview(pool, cfg, log, rend))

		// Authenticated routes
		r.Group(func(r chi.Router) {
			r.Use(handler.RequireAuth(pool))

			r.Get("/me", handler.Me(pool))
			r.Post("/telegram/link-code", handler.TelegramLinkCode(pool))
			r.Get("/telegram/link", handler.TelegramLinkStatus(pool))
			r.Get("/notifications", handler.NotificationsList(pool))

			r.Route("/trackers", func(r chi.Router) {
				r.Get("/", handler.ListTrackers(pool))
				r.Post("/", handler.CreateTracker(pool, cfg))
				r.Get("/{id}", handler.GetTracker(pool))
				r.Patch("/{id}", handler.UpdateTracker(pool))
				r.Delete("/{id}", handler.DeleteTracker(pool))
				r.Post("/{id}/pause", handler.PauseTracker(pool))
				r.Post("/{id}/resume", handler.ResumeTracker(pool))
				r.Post("/{id}/recheck", handler.RecheckTracker(pool, cfg))
				r.Get("/{id}/history", handler.TrackerHistory(pool))
			})
		})
	})

	srv := &http.Server{
		Addr:         fmt.Sprintf(":%d", cfg.Port),
		Handler:      r,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 60 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	go func() {
		log.Info().Str("addr", srv.Addr).Msg("api server listening")
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatal().Err(err).Msg("server error")
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Info().Msg("shutting down server...")
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Fatal().Err(err).Msg("server forced to shutdown")
	}
	log.Info().Msg("server stopped")
}
