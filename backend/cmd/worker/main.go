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
	"unicode"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"

	"github.com/littlewell/price-tracker/internal/config"
	"github.com/littlewell/price-tracker/internal/db"
	"github.com/littlewell/price-tracker/internal/extractor"
	"github.com/littlewell/price-tracker/internal/notifier"
	"github.com/littlewell/price-tracker/internal/proxypool"
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

	fetcher := extractor.NewPageFetcher(rend, cfg.ScraperCookies, cfg.ScraperProxy, proxypool.NewStore(pool))
	attributeExtractor := extractor.NewAttribute()
	genericExtractor := extractor.NewGeneric()
	proxyStore := proxypool.NewStore(pool)
	searchFallback := extractor.NewSearchFallback(proxyStore)
	if searchFallback == nil {
		log.Warn().Msg("search fallback disabled: set OPEN_SERP_BASE_URL, SERPER_API_KEY, or SERPAPI_KEY")
	} else {
		log.Info().Str("fallback", searchFallback.Domain()).Msg("search fallback enabled")
	}

	checkTrackers := func() {
		processTrackers(ctx, pool, rend, fetcher, attributeExtractor, genericExtractor, searchFallback, log)
	}

	sendNotifications := func() {
		notif.SendPending(ctx)
	}

	checkTrackers()
	sendNotifications()

	// Runs on its own hourly cadence rather than in the select loop below: a refresh
	// checks aliveness of every pooled proxy (see Store.Refresh), which can take tens of
	// seconds even with bounded concurrency — long enough that folding it into the
	// tracker/notification ticks would delay them.
	go runProxyPoolRefresher(ctx, pool, log)
	go runGeonodeFetcher(ctx, pool, log)

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

func runProxyPoolRefresher(ctx context.Context, pool *pgxpool.Pool, log zerolog.Logger) {
	store := proxypool.NewStore(pool)
	store.Refresh(ctx, log)

	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			store.Refresh(ctx, log)
		case <-ctx.Done():
			return
		}
	}
}

// runGeonodeFetcher pulls geonode.com's free public proxy list into the pool hourly (see
// Store.FetchGeonode) — a separate ticker from runProxyPoolRefresher's aliveness sweep
// since the two do unrelated work (discovering new proxies vs. re-checking known ones)
// and there's no reason to couple their schedules.
func runGeonodeFetcher(ctx context.Context, pool *pgxpool.Pool, log zerolog.Logger) {
	store := proxypool.NewStore(pool)
	store.FetchGeonode(ctx, log)

	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			store.FetchGeonode(ctx, log)
		case <-ctx.Done():
			return
		}
	}
}

func processTrackers(ctx context.Context, pool *pgxpool.Pool, rend *renderer.Renderer, fetcher *extractor.PageFetcher,
	attr *extractor.AttributeExtractor, generic *extractor.GenericExtractor, searchFallback extractor.Extractor, log zerolog.Logger) {

	rows, err := pool.Query(ctx, `
		SELECT id, url, extraction_rule, currency, current_price, previous_price, current_stock_status,
		       consecutive_errors, check_interval_minutes, tracking_mode
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
			previousPrice      *float64
			currentStockStatus string
			consecutiveErrors  int
			checkInterval      int
			trackingMode       string
		)
		if err := rows.Scan(&id, &url, &extractionRuleJSON, &currency, &currentPrice, &previousPrice, &currentStockStatus, &consecutiveErrors, &checkInterval, &trackingMode); err != nil {
			log.Error().Err(err).Msg("failed to scan tracker")
			continue
		}
		if trackingMode == "stock" {
			processStockTracker(ctx, pool, fetcher, id, url, extractionRuleJSON, consecutiveErrors, checkInterval, log)
			continue
		}
		processTracker(ctx, pool, rend, fetcher, attr, generic, searchFallback, id, url, extractionRuleJSON, currency, currentPrice, previousPrice, consecutiveErrors, checkInterval, log)
	}
}

func processStockTracker(ctx context.Context, pool *pgxpool.Pool, fetcher *extractor.PageFetcher,
	id, url string, extractionRuleJSON []byte, consecutiveErrors, checkInterval int, log zerolog.Logger) {
	if checkInterval <= 0 {
		checkInterval = 180
	}

	log.Info().Str("tracker_id", id).Str("url", url).Msg("checking stock tracker")

	body, fetchMethod, err := fetcher.Fetch(url)
	if err != nil {
		log.Error().Err(err).Str("tracker_id", id).Msg("stock fetch failed")
		handleExtractionError(ctx, pool, id, err.Error(), consecutiveErrors, checkInterval, log)
		return
	}

	stockStatus, stockMethod, err := extractor.DetectStockStatus(extractionRuleJSON, body)
	if err != nil {
		log.Error().Err(err).Str("tracker_id", id).Msg("stock detection failed")
		handleExtractionError(ctx, pool, id, err.Error(), consecutiveErrors, checkInterval, log)
		return
	}

	pool.Exec(ctx, `
		INSERT INTO stock_points (id, tracker_id, stock_status, source, status, extraction_method, fetch_method)
		VALUES (gen_random_uuid(), $1, $2, 'worker_check', 'success', $3, $4)
	`, id, stockStatus, stockMethod, fetchMethod)

	var prevStockStatus string
	pool.QueryRow(ctx, `SELECT current_stock_status FROM trackers WHERE id = $1`, id).Scan(&prevStockStatus)

	pool.Exec(ctx, `
		UPDATE trackers SET
			previous_stock_status = current_stock_status,
			current_stock_status = $2,
			last_checked_at = now(),
			next_check_at = now() + ($3 * interval '1 minute'),
			consecutive_errors = 0,
			last_error = NULL,
			updated_at = now()
		WHERE id = $1
	`, id, stockStatus, checkInterval)

	if prevStockStatus != "" && prevStockStatus != stockStatus {
		notifType := "stock_changed"
		if stockStatus == "in_stock" {
			notifType = "back_in_stock"
		} else if stockStatus == "out_of_stock" {
			notifType = "out_of_stock"
		}
		pool.Exec(ctx, `
			INSERT INTO notifications (id, user_id, tracker_id, type, old_stock_status, new_stock_status, status)
			SELECT gen_random_uuid(), user_id, $1, $2, $3, $4, 'pending'
			FROM trackers WHERE id = $1
		`, id, notifType, prevStockStatus, stockStatus)
	}

	log.Info().Str("tracker_id", id).Str("stock_status", stockStatus).Msg("stock tracker checked successfully")
}

func processTracker(ctx context.Context, pool *pgxpool.Pool, rend *renderer.Renderer, fetcher *extractor.PageFetcher,
	attr *extractor.AttributeExtractor, generic *extractor.GenericExtractor, searchFallback extractor.Extractor,
	id, url string, extractionRuleJSON []byte, currency string, currentPrice, previousPrice *float64,
	consecutiveErrors int, checkInterval int, log zerolog.Logger) {
	if checkInterval <= 0 {
		checkInterval = 180
	}

	log.Info().Str("tracker_id", id).Str("url", url).Msg("checking tracker")

	newPrice, newCurrency, stockStatus, extractionMethod, fetchMethod, err := extractTrackerPrice(ctx, rend, fetcher, attr, generic, searchFallback, url, extractionRuleJSON, currency, currentPrice)
	if err != nil {
		log.Error().Err(err).Str("tracker_id", id).Msg("extraction failed")
		handleExtractionError(ctx, pool, id, err.Error(), consecutiveErrors, checkInterval, log)
		return
	}

	pool.Exec(ctx, `
		INSERT INTO price_points (id, tracker_id, price, currency, source, status, extraction_method, fetch_method)
		VALUES (gen_random_uuid(), $1, $2, $3, 'worker_check', 'success', $4, NULLIF($5, ''))
	`, id, newPrice, newCurrency, extractionMethod, fetchMethod)

	pool.Exec(ctx, `
		INSERT INTO stock_points (id, tracker_id, stock_status, source, status)
		VALUES (gen_random_uuid(), $1, $2, 'worker_check', 'success')
	`, id, stockStatus)

	prevPrice := currentPrice
	// This tracker has already had at least one price change recorded (previousPrice is
	// non-nil) only once previous_price has been set by an earlier change — i.e. this is
	// (at least) the second change. On the very first change, previousPrice is still nil
	// here, and "old price" would just equal initial_price the user already knows, so we
	// omit it from the notification (see notifier.go's price_changed_first template).
	isFirstChange := previousPrice == nil

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
		var oldPriceParam *float64
		if !isFirstChange {
			oldPriceParam = prevPrice
		}
		pool.Exec(ctx, `
			INSERT INTO notifications (id, user_id, tracker_id, type, old_price, new_price, currency, status)
			SELECT gen_random_uuid(), user_id, $1, 'price_changed', $2, $3, $4, 'pending'
			FROM trackers WHERE id = $1
		`, id, oldPriceParam, newPrice, newCurrency)
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

// extractTrackerPrice returns (price, currency, stockStatus, extractionMethod,
// fetchMethod, error). fetchMethod (see extractor.FetchMethod* constants) is only
// meaningful when a PageFetcher tier actually produced the page body — it's "" for the
// css_text and search-fallback paths, where extraction_method already fully describes
// how the price was obtained.
func extractTrackerPrice(ctx context.Context, rend *renderer.Renderer, fetcher *extractor.PageFetcher,
	attr *extractor.AttributeExtractor, generic *extractor.GenericExtractor, searchFallback extractor.Extractor,
	url string, extractionRuleJSON []byte, fallbackCurrency string, referencePrice *float64) (float64, string, string, string, string, error) {

	ruleType := extractor.RuleType(extractionRuleJSON)
	if isSearchFallbackRuleType(ruleType) {
		if searchFallback == nil {
			return 0, "", "", "", "", fmt.Errorf("search fallback disabled for %s tracker", ruleType)
		}
		result, err := searchFallback.Extract(nil, url)
		if err != nil {
			return 0, "", "", "", "", fmt.Errorf("search fallback extraction failed: %w", err)
		}
		if result == nil || len(result.Candidates) == 0 {
			return 0, "", "", "", "", fmt.Errorf("search fallback did not find an exact URL price")
		}
		price, currency, stockStatus, extractionMethod, err := finalizePriceResult(result, fallbackCurrency, referencePrice)
		return price, currency, stockStatus, extractionMethod, "", err
	}

	if len(extractionRuleJSON) > 0 && string(extractionRuleJSON) != "{}" {
		var rule cssTextRule
		if err := json.Unmarshal(extractionRuleJSON, &rule); err == nil && rule.Type == "css_text" && rule.Selector != "" {
			price, err := priceFromCSSRule(ctx, rend, url, rule, referencePrice)
			if err != nil {
				return 0, "", "", "", "", err
			}
			return price, fallbackCurrency, "unknown", "css_text", extractor.FetchMethodRender, nil
		}
	}

	body, fetchMethod, err := fetcher.Fetch(url)
	if err != nil {
		// The page itself is unreachable (bot-blocked, timed out, ...), so no
		// HTML-based extractor can help. Search fallbacks do not need the page's
		// current HTML, so they're the only tiers left worth trying here.
		if searchFallback != nil {
			if result, fallbackErr := searchFallback.Extract(nil, url); fallbackErr == nil && len(result.Candidates) > 0 {
				price, currency, stockStatus, extractionMethod, err := finalizePriceResult(result, fallbackCurrency, referencePrice)
				return price, currency, stockStatus, extractionMethod, "", err
			}
		}
		return 0, "", "", "", "", fmt.Errorf("fetch failed: %w", err)
	}

	result, err := attr.Extract(body, url)
	if err != nil || len(result.Candidates) == 0 {
		result, err = generic.Extract(body, url)
	}
	if (err != nil || len(result.Candidates) == 0) && searchFallback != nil {
		result, err = searchFallback.Extract(body, url)
	}

	if err != nil || len(result.Candidates) == 0 {
		return 0, "", "", "", "", fmt.Errorf("extraction failed")
	}

	price, currency, stockStatus, extractionMethod, err := finalizePriceResult(result, fallbackCurrency, referencePrice)
	return price, currency, stockStatus, extractionMethod, fetchMethod, err
}

func isSearchFallbackRuleType(ruleType string) bool {
	switch ruleType {
	case "openserp_search_result", "openserp_extract", "serper_organic_result", "serper_shopping_result", "serpapi_rich_snippet":
		return true
	default:
		return false
	}
}

type cssTextRule struct {
	Type               string `json:"type"`
	Selector           string `json:"selector"`
	ScreenshotSelector string `json:"screenshot_selector"`
	PriceTokenIndex    *int   `json:"price_token_index"`
}

func priceFromCSSRule(ctx context.Context, rend *renderer.Renderer, url string, rule cssTextRule, referencePrice *float64) (float64, error) {
	text, err := rend.TextBySelector(ctx, url, rule.Selector)
	if err != nil {
		return 0, fmt.Errorf("rule extraction failed: %w", err)
	}
	price, ok := parsePriceFromTextAtIndex(text, rule.PriceTokenIndex, referencePrice)
	if !ok {
		return 0, fmt.Errorf("failed to parse price from selected block")
	}

	if referencePrice != nil && priceCents(price) == priceCents(*referencePrice) && rule.ScreenshotSelector != "" {
		if blockText, err := rend.TextBySelector(ctx, url, rule.ScreenshotSelector); err == nil {
			blockPrices := parseCurrencyPriceTokensFromText(blockText)
			minRatio := 0.01
			if len(blockPrices) == 0 {
				blockPrices = parsePriceTokensFromText(blockText)
				minRatio = 0.2
			}
			if salePrice, ok := chooseSalePrice(blockPrices, price, minRatio); ok {
				return salePrice, nil
			}
		}
	}

	return price, nil
}

func chooseSalePrice(prices []float64, referencePrice float64, minRatio float64) (float64, bool) {
	var (
		best  float64
		found bool
	)
	for _, price := range prices {
		if priceCents(price) >= priceCents(referencePrice) {
			continue
		}
		if minRatio > 0 && price < referencePrice*minRatio {
			continue
		}
		if !found || price < best {
			best = price
			found = true
		}
	}
	return best, found
}

func finalizePriceResult(result *extractor.ExtractionResult, fallbackCurrency string, referencePrice *float64) (float64, string, string, string, error) {
	candidate, newPrice, ok := bestPriceCandidate(result.Candidates, referencePrice)
	if !ok {
		return 0, "", "", "", fmt.Errorf("failed to parse price")
	}

	currency := candidate.Currency
	if currency == "" {
		currency = fallbackCurrency
	}
	stockStatus := result.StockStatus
	if stockStatus == "" {
		stockStatus = "unknown"
	}
	return newPrice, currency, stockStatus, extractor.RuleType(candidate.Rule), nil
}

// bestPriceCandidate picks which extraction candidate to trust when Extract returns more
// than one — e.g. a raw vs. minor-units reading of the same ambiguous JSON price field
// (see extractByRegex). Candidates aren't returned in confidence order, so naively taking
// the first one can pick a 100x-scaled misread over the correct value. An unchanged price
// (matching referencePrice) always wins first, since that's the strongest signal nothing
// actually changed; otherwise the highest-confidence candidate is used.
func bestPriceCandidate(candidates []extractor.PriceCandidate, referencePrice *float64) (extractor.PriceCandidate, float64, bool) {
	if referencePrice != nil {
		for _, c := range candidates {
			price, ok := parsePriceFromText(c.Price)
			if ok && priceCents(price) == priceCents(*referencePrice) {
				return c, price, true
			}
		}
	}

	var (
		best      extractor.PriceCandidate
		bestPrice float64
		bestConf  = -1.0
		found     bool
	)
	for _, c := range candidates {
		price, ok := parsePriceFromText(c.Price)
		if !ok {
			continue
		}
		if !found || c.Confidence > bestConf {
			best, bestPrice, bestConf, found = c, price, c.Confidence, true
		}
	}
	return best, bestPrice, found
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
	price, ok := parsePriceFromTextAtIndex(text, nil, nil)
	return price, ok
}

// parsePriceFromTextAtIndex picks a price out of a tracked block's text. The
// block's token count can change between checks when a sibling discount
// element (e.g. a crossed-out original price) appears next to the tracked
// price, which would shift what rests at the recorded tokenIndex. To guard
// against that, an unchanged price (matching referencePrice) always wins,
// and when multiple distinct prices remain the smallest is preferred, since
// a struck-through original price is never lower than the current one.
// tokenIndex is only trusted as a last resort, when neither check applies.
func parsePriceFromTextAtIndex(text string, tokenIndex *int, referencePrice *float64) (float64, bool) {
	prices := parsePriceTokensFromText(text)
	if len(prices) == 0 {
		return 0, false
	}
	if referencePrice != nil {
		for _, price := range prices {
			if priceCents(price) == priceCents(*referencePrice) {
				return price, true
			}
		}
	}
	if len(prices) > 1 {
		min := prices[0]
		for _, price := range prices[1:] {
			if price < min {
				min = price
			}
		}
		return min, true
	}
	if tokenIndex != nil && *tokenIndex >= 0 && *tokenIndex < len(prices) {
		return prices[*tokenIndex], true
	}
	return prices[0], true
}

func parsePriceTokensFromText(text string) []float64 {
	re := regexp.MustCompile(`\d+(?:[\s.,]\d+)*`)
	matches := re.FindAllString(normalizeUnicodeSpaces(text), -1)
	prices := make([]float64, 0, len(matches))
	for _, match := range matches {
		if price, ok := parsePriceToken(match); ok {
			prices = append(prices, price)
		}
	}
	return prices
}

func parseCurrencyPriceTokensFromText(text string) []float64 {
	normalized := normalizeUnicodeSpaces(text)
	patterns := []*regexp.Regexp{
		regexp.MustCompile(`(?i)(\d+(?:[\s.,]\d+)*)\s*(?:zł|pln|€|eur|\$|usd)`),
		regexp.MustCompile(`(?i)(?:zł|pln|€|eur|\$|usd)\s*(\d+(?:[\s.,]\d+)*)`),
	}

	seen := map[int64]bool{}
	var prices []float64
	for _, pattern := range patterns {
		for _, match := range pattern.FindAllStringSubmatch(normalized, -1) {
			if len(match) < 2 {
				continue
			}
			price, ok := parsePriceToken(match[1])
			if !ok {
				continue
			}
			cents := priceCents(price)
			if seen[cents] {
				continue
			}
			seen[cents] = true
			prices = append(prices, price)
		}
	}
	return prices
}

// parsePriceToken normalizes a raw price string that may use either the
// "1,660.00" (US) or "1.660,00" (EU) convention for thousands/decimal
// separators. The rightmost comma or dot is treated as the decimal point
// only when it is followed by 1-2 digits (a plausible cents value);
// otherwise every separator is treated as a thousands grouping.
func parsePriceToken(match string) (float64, bool) {
	normalized := strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return -1
		}
		return r
	}, match)

	decimalIdx := strings.LastIndex(normalized, ",")
	if dot := strings.LastIndex(normalized, "."); dot > decimalIdx {
		decimalIdx = dot
	}

	intPart, fracPart := normalized, ""
	if decimalIdx != -1 {
		if n := len(normalized) - decimalIdx - 1; n >= 1 && n <= 2 {
			intPart, fracPart = normalized[:decimalIdx], normalized[decimalIdx+1:]
		}
	}

	intPart = strings.NewReplacer(",", "", ".", "").Replace(intPart)
	if intPart == "" {
		return 0, false
	}
	cleaned := intPart
	if fracPart != "" {
		cleaned += "." + fracPart
	}

	price, err := strconv.ParseFloat(cleaned, 64)
	return price, err == nil && price > 0
}

func normalizeUnicodeSpaces(text string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return ' '
		}
		return r
	}, text)
}

func priceCents(price float64) int64 {
	return int64(price*100 + 0.5)
}
