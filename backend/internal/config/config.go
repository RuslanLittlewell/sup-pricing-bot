package config

import (
	"os"
	"strconv"
)

type Config struct {
	Env             string
	Port            int
	DatabaseURL     string
	TelegramToken   string
	TelegramWebhook string
	BotUsername     string
	AdminToken      string
	SessionSecret   string
	ScraperCookies  string
	ScraperProxy    string

	AppName    string
	AppURL     string
	CORSOrigin string

	TributeAPIKey              string
	TributeBasicSubscriptionID int64
	TributeProSubscriptionID   int64
}

func Load() *Config {
	cfg := &Config{
		Env:             getEnv("ENV", "development"),
		Port:            getEnvInt("PORT", 8080),
		DatabaseURL:     getEnv("DATABASE_URL", "postgres://postgres:postgres@localhost:5432/price_tracker?sslmode=disable"),
		TelegramToken:   getEnv("TELEGRAM_BOT_TOKEN", ""),
		TelegramWebhook: getEnv("TELEGRAM_WEBHOOK_URL", ""),
		BotUsername:     getEnv("BOT_USERNAME", ""),
		AdminToken:      getEnv("ADMIN_TOKEN", "admin-secret"),
		SessionSecret:   getEnv("SESSION_SECRET", "change-me-in-production"),
		ScraperCookies:  getEnv("SCRAPER_COOKIES_FILE", ""),
		ScraperProxy:    getEnv("SCRAPER_PROXY_URL", ""),
		AppName:         getEnv("APP_NAME", "Price Tracker"),
		AppURL:          getEnv("APP_URL", "http://localhost:3000"),
		CORSOrigin:      getEnv("CORS_ORIGIN", "http://localhost:3000"),

		TributeAPIKey:              getEnv("TRIBUTE_API_KEY", ""),
		TributeBasicSubscriptionID: getEnvInt64("TRIBUTE_BASIC_SUBSCRIPTION_ID", 0),
		TributeProSubscriptionID:   getEnvInt64("TRIBUTE_PRO_SUBSCRIPTION_ID", 0),
	}
	return cfg
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func getEnvInt(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		if i, err := strconv.Atoi(v); err == nil {
			return i
		}
	}
	return fallback
}

func getEnvInt64(key string, fallback int64) int64 {
	if v := os.Getenv(key); v != "" {
		if i, err := strconv.ParseInt(v, 10, 64); err == nil {
			return i
		}
	}
	return fallback
}
