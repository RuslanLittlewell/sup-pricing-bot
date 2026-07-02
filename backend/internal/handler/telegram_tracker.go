package handler

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"

	"github.com/littlewell/price-tracker/internal/extractor"
	"github.com/littlewell/price-tracker/internal/renderer"
	"github.com/littlewell/price-tracker/internal/telegram"
)

// enforceTrackerLimit reports whether the user may create another tracker. When they've
// hit their plan's max_trackers it sends an explanatory message (with an upgrade shortcut)
// and returns false. On a transient DB error it fails open (allows creation) rather than
// blocking a legitimate user.
func enforceTrackerLimit(ctx context.Context, pool *pgxpool.Pool, tg *telegram.Client, chatID int64, userID, lang string, log zerolog.Logger) bool {
	limits := getPlanLimits(ctx, pool, userID)
	count, err := countActiveTrackers(ctx, pool, userID)
	if err != nil {
		log.Error().Err(err).Str("user_id", userID).Msg("failed to count trackers for limit check")
		return true
	}
	if count >= limits.maxTrackers {
		markup := makeInlineKeyboard(
			[]inlineButton{button(tr(lang, "button_plans"), "menu:plans")},
			[]inlineButton{button(tr(lang, "button_back"), "menu:back")},
		)
		_ = tg.SendMessageWithMarkup(chatID, fmt.Sprintf(tr(lang, "tracker_limit_reached"), limits.maxTrackers), markup)
		return false
	}
	return true
}

func updateTrackerInterval(ctx context.Context, pool *pgxpool.Pool, tg *telegram.Client, chatID int64, userID, lang, trackerID string, minutes int, log zerolog.Logger) {
	if minutes > 1440 {
		minutes = 1440
	}
	// Enforce the plan's minimum. The interval menu only offers allowed values, but the
	// user can also type a number in the awaiting_interval step — reject anything faster
	// than their plan permits rather than silently accepting it.
	minInterval := getPlanLimits(ctx, pool, userID).minIntervalMinutes
	if minutes < minInterval {
		markup := makeInlineKeyboard([]inlineButton{button(tr(lang, "button_plans"), "menu:plans")})
		_ = tg.SendMessageWithMarkup(chatID, fmt.Sprintf(tr(lang, "interval_below_min"), formatInterval(lang, minInterval)), markup)
		return
	}
	tag, err := pool.Exec(ctx, `
		UPDATE trackers
		SET check_interval_minutes = $3,
		    next_check_at = now() + make_interval(mins => $3),
		    updated_at = now()
		WHERE id::text LIKE $1 || '%' AND user_id = $2 AND status != 'deleted'
	`, trackerID, userID, minutes)
	if err != nil {
		log.Error().
			Err(err).
			Str("user_id", userID).
			Str("tracker_id", trackerID).
			Int("minutes", minutes).
			Msg("failed to update tracker interval")
		SendTelegramMessage(tg, chatID, tr(lang, "interval_save_failed"))
		return
	}
	if tag.RowsAffected() == 0 {
		SendTelegramMessage(tg, chatID, tr(lang, "tracker_not_found"))
		return
	}
	markup := makeInlineKeyboard(
		[]inlineButton{button(tr(lang, "button_trackers"), "menu:list")},
		[]inlineButton{button(tr(lang, "button_back"), "menu:list")},
	)
	_ = tg.SendMessageWithMarkup(chatID, fmt.Sprintf(tr(lang, "interval_saved"), formatInterval(lang, minutes)), markup)
}

func createTrackerFromState(ctx context.Context, pool *pgxpool.Pool, tg *telegram.Client, chatID int64, userID, lang string, state telegramState, log zerolog.Logger) {
	// Final guard: the limit is also checked when the flow starts, but /add and the menu
	// flow are independent entry points, so re-check right before the insert.
	if !enforceTrackerLimit(ctx, pool, tg, chatID, userID, lang, log) {
		clearTelegramState(ctx, pool, chatID)
		return
	}
	title := state.Title
	if title == "" {
		title = extractDomain(state.URL)
	}

	var trackerID string
	err := pool.QueryRow(ctx, `
		INSERT INTO trackers (user_id, url, normalized_url, domain, title, initial_price, current_price, currency, current_stock_status, extraction_rule, extraction_confidence, status, next_check_at)
		VALUES ($1, $2, $2, $3, $4, $5, $5, $6, 'unknown', $7, 0.9, 'active', now() + interval '3 hours')
		RETURNING id
	`, userID, state.URL, extractDomain(state.URL), title, state.InitialPrice, state.Currency, state.Rule).Scan(&trackerID)
	if err != nil {
		log.Error().Err(err).Msg("failed to create tracker from telegram state")
		SendTelegramMessage(tg, chatID, tr(lang, "tracker_create_failed"))
		return
	}
	log.Info().
		Int64("telegram_id", chatID).
		Str("user_id", userID).
		Str("tracker_id", trackerID).
		Str("title", title).
		Str("url", state.URL).
		Float64("price", state.InitialPrice).
		Str("currency", state.Currency).
		Msg("telegram tracker created")

	_, _ = pool.Exec(ctx, `
		INSERT INTO price_points (id, tracker_id, price, currency, source, status)
		VALUES (gen_random_uuid(), $1, $2, $3, 'bot_add', 'success')
	`, trackerID, state.InitialPrice, state.Currency)

	clearTelegramState(ctx, pool, chatID)
	markup := makeInlineKeyboard(
		[]inlineButton{button(tr(lang, "button_trackers"), "menu:list")},
		[]inlineButton{button(tr(lang, "button_back"), "menu:back")},
	)
	_ = tg.SendMessageWithMarkup(chatID, fmt.Sprintf(tr(lang, "tracker_created"), title, state.URL, formatMoney(state.InitialPrice), trackerID[:8]), markup)
}

func createStockTrackerFromURL(ctx context.Context, pool *pgxpool.Pool, tg *telegram.Client, chatID int64, userID, lang, url string, log zerolog.Logger, fetcher *extractor.PageFetcher) {
	if !enforceTrackerLimit(ctx, pool, tg, chatID, userID, lang, log) {
		clearTelegramState(ctx, pool, chatID)
		return
	}
	SendTelegramMessage(tg, chatID, tr(lang, "stock_search_started"))

	body, err := fetcher.Fetch(url)
	if err != nil {
		log.Error().Err(err).Str("url", url).Msg("failed to fetch page for stock tracker")
		SendTelegramMessage(tg, chatID, fmt.Sprintf(tr(lang, "page_load_failed_detail"), err.Error()))
		clearTelegramState(ctx, pool, chatID)
		return
	}

	stockStatus := extractor.DetectStockStatusFromText(body)

	generic := extractor.NewGeneric()
	result, _ := generic.Extract(body, url)
	title := ""
	if result != nil {
		title = result.Title
	}
	if title == "" {
		title = extractDomain(url)
	}

	var trackerID string
	err = pool.QueryRow(ctx, `
		INSERT INTO trackers (user_id, url, normalized_url, domain, title, initial_price, current_price, currency, current_stock_status, tracking_mode, status, next_check_at)
		VALUES ($1, $2, $2, $3, $4, 0, NULL, 'PLN', $5, 'stock', 'active', now() + interval '3 hours')
		RETURNING id
	`, userID, url, extractDomain(url), title, stockStatus).Scan(&trackerID)
	if err != nil {
		log.Error().Err(err).Msg("failed to create stock tracker")
		SendTelegramMessage(tg, chatID, tr(lang, "tracker_create_failed"))
		clearTelegramState(ctx, pool, chatID)
		return
	}
	log.Info().
		Int64("telegram_id", chatID).
		Str("user_id", userID).
		Str("tracker_id", trackerID).
		Str("title", title).
		Str("url", url).
		Str("stock_status", stockStatus).
		Msg("telegram stock tracker created")

	pool.Exec(ctx, `
		INSERT INTO stock_points (id, tracker_id, stock_status, source, status)
		VALUES (gen_random_uuid(), $1, $2, 'bot_add', 'success')
	`, trackerID, stockStatus)

	clearTelegramState(ctx, pool, chatID)
	markup := makeInlineKeyboard(
		[]inlineButton{button(tr(lang, "button_trackers"), "menu:list")},
		[]inlineButton{button(tr(lang, "button_back"), "menu:back")},
	)
	_ = tg.SendMessageWithMarkup(chatID, fmt.Sprintf(tr(lang, "stock_tracker_created"), title, url, stockStatus, trackerID[:8]), markup)
}

type trackerListRow struct {
	id, url, title, currency, stockStatus, status string
	price                                         *float64
	interval                                      int
}

func handleListTrackers(ctx context.Context, pool *pgxpool.Pool, tg *telegram.Client, chatID int64, userID, lang string, log zerolog.Logger) {
	rows, err := pool.Query(ctx, `
		SELECT id, url, COALESCE(title, domain), current_price, currency, current_stock_status, status, check_interval_minutes
		FROM trackers WHERE user_id = $1 AND status != 'deleted' ORDER BY created_at DESC
	`, userID)
	if err != nil {
		SendTelegramMessage(tg, chatID, tr(lang, "trackers_load_failed"))
		return
	}
	defer rows.Close()

	var trackers []trackerListRow
	for rows.Next() {
		var t trackerListRow
		rows.Scan(&t.id, &t.url, &t.title, &t.price, &t.currency, &t.stockStatus, &t.status, &t.interval)
		trackers = append(trackers, t)
	}

	if len(trackers) == 0 {
		sendMainMenu(tg, chatID, lang, tr(lang, "trackers_empty"))
		return
	}

	for i, t := range trackers {
		shortID := t.id[:8]

		card := fmt.Sprintf("🔹 <b>%s</b>", truncate(t.title, 60))
		if t.price != nil {
			card += fmt.Sprintf("\n💰 %.2f %s", *t.price, t.currency)
		}
		card += fmt.Sprintf("\n⏱ %s", formatInterval(lang, t.interval))
		card += fmt.Sprintf("\n🔗 %s", extractDomain(t.url))
		card += fmt.Sprintf("\nID: <code>%s</code>", shortID)

		kbd := [][]inlineButton{
			{button(tr(lang, "button_edit"), "tracker:edit:"+shortID), button(tr(lang, "button_delete"), "tracker:delete:"+shortID)},
		}
		if i == len(trackers)-1 {
			kbd = append(kbd, []inlineButton{button(tr(lang, "button_back"), "menu:back")})
		}

		_ = tg.SendMessageWithMarkup(chatID, card, makeInlineKeyboard(kbd...))
	}
}

func handleAddTracker(ctx context.Context, pool *pgxpool.Pool, tg *telegram.Client, chatID int64, userID, lang, url string, log zerolog.Logger, rend *renderer.Renderer, cookiesFile, proxyURL string) {
	if !enforceTrackerLimit(ctx, pool, tg, chatID, userID, lang, log) {
		return
	}
	fetcher := extractor.NewPageFetcher(rend, cookiesFile, proxyURL)
	body, err := fetcher.Fetch(url)
	if err != nil {
		if serp := extractor.NewSerpAPI(); serp != nil {
			if result, serpErr := serp.Extract(nil, url); serpErr == nil && len(result.Candidates) > 0 {
				finishAddTracker(ctx, pool, tg, chatID, userID, lang, url, result, log)
				return
			}
		}
		SendTelegramMessage(tg, chatID, fmt.Sprintf(tr(lang, "page_load_failed_detail"), err.Error()))
		return
	}

	attr := extractor.NewAttribute()
	result, err := attr.Extract(body, url)
	if err != nil || len(result.Candidates) == 0 {
		generic := extractor.NewGeneric()
		result, err = generic.Extract(body, url)
	}
	if err != nil || len(result.Candidates) == 0 {
		if serp := extractor.NewSerpAPI(); serp != nil {
			result, err = serp.Extract(body, url)
		}
	}
	if err != nil || len(result.Candidates) == 0 {
		SendTelegramMessage(tg, chatID, tr(lang, "extract_price_failed"))
		return
	}

	finishAddTracker(ctx, pool, tg, chatID, userID, lang, url, result, log)
}

// finishAddTracker is the shared tail of handleAddTracker: it persists the first
// candidate from an already-produced ExtractionResult as a new tracker. Shared between
// the normal path (page fetched, ran through attribute-based/generic/SerpApi) and the fetch-failed
// path (page unreachable, only SerpApi's cached rich snippet was available).
func finishAddTracker(ctx context.Context, pool *pgxpool.Pool, tg *telegram.Client, chatID int64, userID, lang, url string, result *extractor.ExtractionResult, log zerolog.Logger) {
	candidate := result.Candidates[0]
	newPrice := 0.0
	fmt.Sscanf(candidate.Price, "%f", &newPrice)

	var trackerID string
	err := pool.QueryRow(ctx, `
		INSERT INTO trackers (user_id, url, normalized_url, domain, title, image_url, initial_price, current_price, currency, current_stock_status, extraction_rule, extraction_confidence, status)
		VALUES ($1, $2, $2, $3, $4, $5, $6, $6, $7, $8, $9, $10, 'active')
		RETURNING id
	`, userID, url, extractDomain(url), result.Title, result.ImageURL, newPrice, candidate.Currency, result.StockStatus, candidate.Rule, candidate.Confidence).Scan(&trackerID)
	if err != nil {
		log.Error().Err(err).Msg("failed to create tracker")
		SendTelegramMessage(tg, chatID, tr(lang, "tracker_create_failed"))
		return
	}

	// Save initial price point
	pool.Exec(ctx, `
		INSERT INTO price_points (id, tracker_id, price, currency, source, status)
		VALUES (gen_random_uuid(), $1, $2, $3, 'bot_add', 'success')
	`, trackerID, newPrice, candidate.Currency)

	pool.Exec(ctx, `
		INSERT INTO stock_points (id, tracker_id, stock_status, source, status)
		VALUES (gen_random_uuid(), $1, $2, 'bot_add', 'success')
	`, trackerID, result.StockStatus)

	title := result.Title
	if title == "" {
		title = extractDomain(url)
	}
	SendTelegramMessage(tg, chatID, fmt.Sprintf(tr(lang, "tracker_created_auto"), title, formatMoney(newPrice), result.StockStatus, trackerID[:8]))
}

func handleDeleteTracker(ctx context.Context, pool *pgxpool.Pool, tg *telegram.Client, chatID int64, userID, lang, trackerID string, log zerolog.Logger) {
	tag, err := pool.Exec(ctx, `DELETE FROM trackers WHERE id::text LIKE $1 || '%' AND user_id = $2`, trackerID, userID)
	if err != nil {
		SendTelegramMessage(tg, chatID, tr(lang, "tracker_delete_failed"))
		return
	}
	if tag.RowsAffected() == 0 {
		SendTelegramMessage(tg, chatID, tr(lang, "tracker_not_found"))
		return
	}
	SendTelegramMessage(tg, chatID, tr(lang, "tracker_deleted"))
}

func handleCheckTracker(ctx context.Context, pool *pgxpool.Pool, tg *telegram.Client, chatID int64, userID, lang, trackerID string, log zerolog.Logger, rend *renderer.Renderer, cookiesFile, proxyURL string) {
	var url, currency, trackingMode string
	err := pool.QueryRow(ctx, `SELECT url, currency, tracking_mode FROM trackers WHERE id::text LIKE $1 || '%' AND user_id = $2`, trackerID, userID).Scan(&url, &currency, &trackingMode)
	if err != nil {
		SendTelegramMessage(tg, chatID, tr(lang, "tracker_not_found"))
		return
	}

	fetcher := extractor.NewPageFetcher(rend, cookiesFile, proxyURL)
	body, err := fetcher.Fetch(url)
	if err != nil {
		if trackingMode != "stock" {
			if serp := extractor.NewSerpAPI(); serp != nil {
				if result, serpErr := serp.Extract(nil, url); serpErr == nil && len(result.Candidates) > 0 {
					finishCheckTracker(ctx, pool, tg, chatID, lang, trackerID, currency, result)
					return
				}
			}
		}
		SendTelegramMessage(tg, chatID, tr(lang, "page_load_failed"))
		return
	}

	if trackingMode == "stock" {
		stockStatus := extractor.DetectStockStatusFromText(body)
		pool.Exec(ctx, `INSERT INTO stock_points (id, tracker_id, stock_status, source, status) VALUES (gen_random_uuid(), $1, $2, 'manual_check', 'success')`, trackerID, stockStatus)
		pool.Exec(ctx, `UPDATE trackers SET previous_stock_status = current_stock_status, current_stock_status = $2, last_checked_at = now(), next_check_at = now() + (check_interval_minutes * interval '1 minute'), consecutive_errors = 0, last_error = NULL WHERE id::text LIKE $1 || '%'`, trackerID, stockStatus)
		SendTelegramMessage(tg, chatID, fmt.Sprintf(tr(lang, "manual_check_done_stock"), stockStatus))
		return
	}

	attr := extractor.NewAttribute()
	result, err := attr.Extract(body, url)
	if err != nil || len(result.Candidates) == 0 {
		generic := extractor.NewGeneric()
		result, err = generic.Extract(body, url)
	}
	if err != nil || len(result.Candidates) == 0 {
		if serp := extractor.NewSerpAPI(); serp != nil {
			result, err = serp.Extract(body, url)
		}
	}
	if err != nil || len(result.Candidates) == 0 {
		SendTelegramMessage(tg, chatID, tr(lang, "extract_failed"))
		return
	}

	finishCheckTracker(ctx, pool, tg, chatID, lang, trackerID, currency, result)
}

// finishCheckTracker is the shared tail of handleCheckTracker: it persists the first
// candidate from an already-produced ExtractionResult as the tracker's latest reading.
// Shared between the normal path (page fetched) and the fetch-failed path (page
// unreachable, only SerpApi's cached rich snippet was available).
func finishCheckTracker(ctx context.Context, pool *pgxpool.Pool, tg *telegram.Client, chatID int64, lang, trackerID, fallbackCurrency string, result *extractor.ExtractionResult) {
	candidate := result.Candidates[0]
	newPrice := 0.0
	fmt.Sscanf(candidate.Price, "%f", &newPrice)

	pool.Exec(ctx, `INSERT INTO price_points (id, tracker_id, price, currency, source, status) VALUES (gen_random_uuid(), $1, $2, $3, 'manual_check', 'success')`, trackerID, newPrice, candidate.Currency)
	pool.Exec(ctx, `INSERT INTO stock_points (id, tracker_id, stock_status, source, status) VALUES (gen_random_uuid(), $1, $2, 'manual_check', 'success')`, trackerID, result.StockStatus)
	nextCurrency := candidate.Currency
	if nextCurrency == "" {
		nextCurrency = fallbackCurrency
	}
	pool.Exec(ctx, `UPDATE trackers SET current_price = $2, current_stock_status = $3, currency = $4, last_checked_at = now(), next_check_at = now() + (check_interval_minutes * interval '1 minute'), consecutive_errors = 0, last_error = NULL WHERE id::text LIKE $1 || '%'`, trackerID, newPrice, result.StockStatus, nextCurrency)

	SendTelegramMessage(tg, chatID, fmt.Sprintf(tr(lang, "manual_check_done"), formatMoney(newPrice), result.StockStatus))
}

func handleTrackerHistory(ctx context.Context, pool *pgxpool.Pool, tg *telegram.Client, chatID int64, userID, lang, trackerID string, log zerolog.Logger) {
	var title string
	err := pool.QueryRow(ctx, `SELECT title FROM trackers WHERE id::text LIKE $1 || '%' AND user_id = $2`, trackerID, userID).Scan(&title)
	if err != nil {
		SendTelegramMessage(tg, chatID, tr(lang, "tracker_not_found"))
		return
	}

	priceRows, err := pool.Query(ctx, `
		SELECT price, currency, checked_at FROM price_points
		WHERE tracker_id = $1 ORDER BY checked_at DESC LIMIT 10
	`, trackerID)
	if err != nil {
		SendTelegramMessage(tg, chatID, tr(lang, "history_load_failed"))
		return
	}
	defer priceRows.Close()

	var lines []string
	lines = append(lines, fmt.Sprintf(tr(lang, "history_title"), truncate(title, 30)))

	hasData := false
	for priceRows.Next() {
		hasData = true
		var price float64
		var currency string
		var checkedAt interface{}
		priceRows.Scan(&price, &currency, &checkedAt)
		lines = append(lines, formatMoney(price))
	}

	if !hasData {
		lines = append(lines, tr(lang, "history_empty"))
	}

	SendTelegramMessage(tg, chatID, strings.Join(lines, "\n"))
}
