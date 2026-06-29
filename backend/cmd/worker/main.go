package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"

	"github.com/littlewell/price-tracker/internal/config"
	"github.com/littlewell/price-tracker/internal/db"
	"github.com/littlewell/price-tracker/internal/extractor"
	"github.com/littlewell/price-tracker/internal/notifier"
	"github.com/littlewell/price-tracker/internal/renderer"
	"github.com/littlewell/price-tracker/internal/telegram"
)

func main() {
	log := zerolog.New(zerolog.ConsoleWriter{Out: os.Stdout, TimeFormat: time.RFC3339}).
		With().Timestamp().Logger()

	cfg := config.Load()

	tg := telegram.NewClient(cfg.TelegramToken)

	log.Info().Msg("starting worker")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	pool, err := db.ConnectPool(ctx, cfg.DatabaseURL, log)
	if err != nil {
		log.Fatal().Err(err).Msg("failed to connect to database")
	}
	defer pool.Close()

	ctx, cancel = context.WithCancel(context.Background())
	defer cancel()

	rend, err := renderer.New(cfg.ScraperCookies, cfg.ScraperProxy)
	if err != nil {
		log.Fatal().Err(err).Msg("failed to start headless browser")
	}
	defer rend.Close()

	notif := notifier.New(pool, tg, log)

	trackerTicker := time.NewTicker(30 * time.Second)
	notifTicker := time.NewTicker(15 * time.Second)
	defer trackerTicker.Stop()
	defer notifTicker.Stop()

	fetcher := extractor.NewPageFetcher(rend, cfg.ScraperCookies, cfg.ScraperProxy)
	zaraExtractor := extractor.NewZara()
	genericExtractor := extractor.NewGeneric()

	checkTrackers := func() {
		processTrackers(ctx, pool, rend, fetcher, zaraExtractor, genericExtractor, log)
	}

	sendNotifications := func() {
		notif.SendPending(ctx)
	}

	checkTrackers()
	sendNotifications()

	for {
		select {
		case <-trackerTicker.C:
			checkTrackers()
		case <-notifTicker.C:
			sendNotifications()
		case <-ctx.Done():
			log.Info().Msg("worker shutting down")
			return
		}
	}
}

func processTrackers(ctx context.Context, pool *pgxpool.Pool, rend *renderer.Renderer, fetcher *extractor.PageFetcher,
	zara *extractor.ZaraExtractor, generic *extractor.GenericExtractor, log zerolog.Logger) {

	rows, err := pool.Query(ctx, `
		SELECT id, url, extraction_rule, currency, current_price, current_stock_status,
		       consecutive_errors, check_interval_minutes
		FROM trackers
		WHERE status = 'active' AND next_check_at <= now()
		ORDER BY next_check_at
		LIMIT 10
		FOR UPDATE SKIP LOCKED
	`)
	if err != nil {
		log.Error().Err(err).Msg("failed to query trackers")
		return
	}
	defer rows.Close()

	for rows.Next() {
		var (
			id                 string
			url                string
			extractionRuleJSON []byte
			currency           string
			currentPrice       *float64
			currentStockStatus string
			consecutiveErrors  int
			checkInterval      int
		)
		if err := rows.Scan(&id, &url, &extractionRuleJSON, &currency, &currentPrice, &currentStockStatus, &consecutiveErrors, &checkInterval); err != nil {
			log.Error().Err(err).Msg("failed to scan tracker")
			continue
		}
		processTracker(ctx, pool, rend, fetcher, zara, generic, id, url, extractionRuleJSON, currency, currentPrice, consecutiveErrors, checkInterval, log)
	}
}

func processTracker(ctx context.Context, pool *pgxpool.Pool, rend *renderer.Renderer, fetcher *extractor.PageFetcher,
	zara *extractor.ZaraExtractor, generic *extractor.GenericExtractor,
	id, url string, extractionRuleJSON []byte, currency string, currentPrice *float64,
	consecutiveErrors int, checkInterval int, log zerolog.Logger) {
	if checkInterval <= 0 {
		checkInterval = 180
	}

	log.Info().Str("tracker_id", id).Str("url", url).Msg("checking tracker")

	newPrice, newCurrency, stockStatus, err := extractTrackerPrice(ctx, rend, fetcher, zara, generic, url, extractionRuleJSON, currency)
	if err != nil {
		log.Error().Err(err).Str("tracker_id", id).Msg("extraction failed")
		handleExtractionError(ctx, pool, id, err.Error(), consecutiveErrors, checkInterval, log)
		return
	}

	pool.Exec(ctx, `
		INSERT INTO price_points (id, tracker_id, price, currency, source, status)
		VALUES (gen_random_uuid(), $1, $2, $3, 'worker_check', 'success')
	`, id, newPrice, newCurrency)

	pool.Exec(ctx, `
		INSERT INTO stock_points (id, tracker_id, stock_status, source, status)
		VALUES (gen_random_uuid(), $1, $2, 'worker_check', 'success')
	`, id, stockStatus)

	prevPrice := currentPrice

	pool.Exec(ctx, `
		UPDATE trackers SET
			previous_price = current_price,
			current_price = $2,
			previous_stock_status = current_stock_status,
			current_stock_status = $3,
			last_checked_at = now(),
			next_check_at = now() + ($4 * interval '1 minute'),
			consecutive_errors = 0,
			last_error = NULL,
			updated_at = now()
		WHERE id = $1
	`, id, newPrice, stockStatus, checkInterval)

	if prevPrice != nil && *prevPrice != newPrice {
		pool.Exec(ctx, `
			INSERT INTO notifications (id, user_id, tracker_id, type, old_price, new_price, currency, status)
			SELECT gen_random_uuid(), user_id, $1, 'price_changed', $2, $3, $4, 'pending'
			FROM trackers WHERE id = $1
		`, id, *prevPrice, newPrice, newCurrency)
	}

	var prevStockStatus string
	pool.QueryRow(ctx, `SELECT previous_stock_status FROM trackers WHERE id = $1`, id).Scan(&prevStockStatus)
	if prevStockStatus != "" && prevStockStatus != stockStatus {
		notifType := "stock_changed"
		if stockStatus == "in_stock" {
			notifType = "back_in_stock"
		} else if stockStatus == "out_of_stock" {
			notifType = "out_of_stock"
		}
		pool.Exec(ctx, `
			INSERT INTO notifications (id, user_id, tracker_id, type, old_stock_status, new_stock_status, currency, status)
			SELECT gen_random_uuid(), user_id, $1, $2, $3, $4, $5, 'pending'
			FROM trackers WHERE id = $1
		`, id, notifType, prevStockStatus, stockStatus, newCurrency)
	}

	log.Info().Str("tracker_id", id).Float64("price", newPrice).Msg("tracker checked successfully")
}

func extractTrackerPrice(ctx context.Context, rend *renderer.Renderer, fetcher *extractor.PageFetcher,
	zara *extractor.ZaraExtractor, generic *extractor.GenericExtractor,
	url string, extractionRuleJSON []byte, fallbackCurrency string) (float64, string, string, error) {

	if len(extractionRuleJSON) > 0 && string(extractionRuleJSON) != "{}" {
		var rule struct {
			Type     string `json:"type"`
			Selector string `json:"selector"`
		}
		if err := json.Unmarshal(extractionRuleJSON, &rule); err == nil && rule.Type == "css_text" && rule.Selector != "" {
			text, err := rend.TextBySelector(ctx, url, rule.Selector)
			if err != nil {
				return 0, "", "", fmt.Errorf("rule extraction failed: %w", err)
			}
			price, ok := parsePriceFromText(text)
			if !ok {
				return 0, "", "", fmt.Errorf("failed to parse price from selected block")
			}
			return price, fallbackCurrency, "unknown", nil
		}
	}

	body, err := fetcher.Fetch(url)
	if err != nil {
		return 0, "", "", fmt.Errorf("fetch failed: %w", err)
	}

	result, err := zara.Extract(body, url)
	if err != nil || len(result.Candidates) == 0 {
		result, err = generic.Extract(body, url)
	}

	if err != nil || len(result.Candidates) == 0 {
		return 0, "", "", fmt.Errorf("extraction failed")
	}

	candidate := result.Candidates[0]
	newPrice := 0.0
	if _, err := fmt.Sscanf(candidate.Price, "%f", &newPrice); err != nil {
		return 0, "", "", fmt.Errorf("failed to parse price")
	}

	currency := candidate.Currency
	if currency == "" {
		currency = fallbackCurrency
	}
	stockStatus := result.StockStatus
	if stockStatus == "" {
		stockStatus = "unknown"
	}
	return newPrice, currency, stockStatus, nil
}

func handleExtractionError(ctx context.Context, pool *pgxpool.Pool, id, errMsg string, consecutiveErrors int, checkInterval int, log zerolog.Logger) {
	if checkInterval <= 0 {
		checkInterval = 180
	}
	newConsecutive := consecutiveErrors + 1
	newStatus := "active"
	if newConsecutive >= 3 {
		newStatus = "needs_confirmation"
	}

	pool.Exec(ctx, `
		UPDATE trackers SET
			consecutive_errors = $2,
			last_error = $3,
			last_checked_at = now(),
			next_check_at = now() + ($5 * interval '1 minute'),
			status = $4,
			updated_at = now()
		WHERE id = $1
	`, id, newConsecutive, errMsg, newStatus, checkInterval)

	pool.Exec(ctx, `
		INSERT INTO price_points (id, tracker_id, price, currency, source, status, error_message)
		VALUES (gen_random_uuid(), $1, NULL, '', 'worker_check', 'failed', $2)
	`, id, errMsg)

	log.Warn().Str("tracker_id", id).Int("consecutive", newConsecutive).Msg("extraction error recorded")
}

func parsePriceFromText(text string) (float64, bool) {
	re := regexp.MustCompile(`\d+(?:[\s.,]\d+)*`)
	match := re.FindString(text)
	if match == "" {
		return 0, false
	}
	normalized := strings.ReplaceAll(match, " ", "")
	if strings.Count(normalized, ",") == 1 && strings.Count(normalized, ".") == 0 {
		normalized = strings.ReplaceAll(normalized, ",", ".")
	} else {
		normalized = strings.ReplaceAll(normalized, ",", "")
	}
	price, err := strconv.ParseFloat(normalized, 64)
	return price, err == nil && price > 0
}
