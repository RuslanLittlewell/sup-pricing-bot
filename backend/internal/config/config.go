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

	AppName    string
	AppURL     string
	CORSOrigin string
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
		AppName:         getEnv("APP_NAME", "Price Tracker"),
		AppURL:          getEnv("APP_URL", "http://localhost:3000"),
		CORSOrigin:      getEnv("CORS_ORIGIN", "http://localhost:3000"),
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
