package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"

	"github.com/littlewell/price-tracker/internal/extractor"
	"github.com/littlewell/price-tracker/internal/proxypool"
	"github.com/littlewell/price-tracker/internal/renderer"
	"github.com/littlewell/price-tracker/internal/shops"
	"github.com/littlewell/price-tracker/internal/telegram"
)

// recordExtractionFailure logs a price search that failed before any tracker row could
// be created (e.g. /add or the interactive add-by-price flow, when every extractor comes
// up empty) — so it still shows up in the admin dashboard's failed-links list, instead of
// vanishing with nothing to attach the failure to (trackers.last_error only exists once a
// tracker has actually been created).
func recordExtractionFailure(ctx context.Context, pool *pgxpool.Pool, userID, url, errMsg string, log zerolog.Logger) {
	if _, err := pool.Exec(ctx, `
		INSERT INTO extraction_failures (id, user_id, url, error)
		VALUES (gen_random_uuid(), $1, $2, $3)
	`, userID, url, errMsg); err != nil {
		log.Error().Err(err).Str("url", url).Msg("failed to record extraction failure")
	}
}

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
		var rows [][]inlineButton
		if limits.code == "free" && !hasUsedTrial(ctx, pool, userID) {
			rows = append(rows, []inlineButton{button(tr(lang, "button_activate_trial"), "activate_trial")})
		}
		rows = append(rows,
			[]inlineButton{button(tr(lang, "button_plans"), "menu:plans")},
			[]inlineButton{button(tr(lang, "button_back"), "menu:back")},
		)
		_ = tg.SendMessageWithMarkup(chatID, fmt.Sprintf(tr(lang, "tracker_limit_reached"), limits.maxTrackers), makeInlineKeyboard(rows...))
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
	limits := getPlanLimits(ctx, pool, userID)
	minInterval := limits.minIntervalMinutes
	if minutes < minInterval {
		var rows [][]inlineButton
		if limits.code == "free" && !hasUsedTrial(ctx, pool, userID) {
			rows = append(rows, []inlineButton{button(tr(lang, "button_activate_trial"), "activate_trial")})
		}
		rows = append(rows, []inlineButton{button(tr(lang, "button_plans"), "menu:plans")})
		_ = tg.SendMessageWithMarkup(chatID, fmt.Sprintf(tr(lang, "interval_below_min"), formatInterval(lang, minInterval)), makeInlineKeyboard(rows...))
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
		INSERT INTO price_points (id, tracker_id, price, currency, source, status, extraction_method, fetch_method)
		VALUES (gen_random_uuid(), $1, $2, $3, 'bot_add', 'success', $4, NULLIF($5, ''))
	`, trackerID, state.InitialPrice, state.Currency, extractor.RuleType(state.Rule), state.FetchMethod)

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

	body, fetchMethod, err := fetcher.Fetch(url)
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
		INSERT INTO stock_points (id, tracker_id, stock_status, source, status, extraction_method, fetch_method)
		VALUES (gen_random_uuid(), $1, $2, 'bot_add', 'success', 'keyword_scan', $3)
	`, trackerID, stockStatus, fetchMethod)

	clearTelegramState(ctx, pool, chatID)
	markup := makeInlineKeyboard(
		[]inlineButton{button(tr(lang, "button_trackers"), "menu:list")},
		[]inlineButton{button(tr(lang, "button_back"), "menu:back")},
	)
	_ = tg.SendMessageWithMarkup(chatID, fmt.Sprintf(tr(lang, "stock_tracker_created"), title, url, stockStatus, trackerID[:8]), markup)
}

// startStockTracking is the entry point for the "track availability" flow. Most sites
// only expose a whole-item in-stock/out-of-stock signal, but some (Zara, so far) publish
// per-size availability in a structured block on the page — for those, offer a size
// picker instead of tracking the item as a whole, since "back in stock" in one size the
// user doesn't want isn't the notification they're after.
func startStockTracking(ctx context.Context, pool *pgxpool.Pool, tg *telegram.Client, chatID int64, userID, lang, url string, log zerolog.Logger, fetcher *extractor.PageFetcher) {
	if shops.IsZaraURL(url) && offerZaraSizeSelection(ctx, pool, tg, chatID, userID, lang, url, log, fetcher) {
		return
	}
	createStockTrackerFromURL(ctx, pool, tg, chatID, userID, lang, url, log, fetcher)
}

// zaraSizeSelectionState is what offerZaraSizeSelection stashes in telegram_states.rule
// while waiting for the user to pick a size (see the "size:" callback in telegram.go).
type zaraSizeSelectionState struct {
	FetchMethod string                  `json:"fetch_method"`
	Variants    []shops.ZaraSizeVariant `json:"variants"`
}

// offerZaraSizeSelection shows a button per out-of-stock size and returns true, so the
// caller stops here instead of falling back to whole-item tracking. Returns false (page
// unreachable, no size data on it, or every size already in stock) to let the caller fall
// back to the generic flow.
func offerZaraSizeSelection(ctx context.Context, pool *pgxpool.Pool, tg *telegram.Client, chatID int64, userID, lang, url string, log zerolog.Logger, fetcher *extractor.PageFetcher) bool {
	SendTelegramMessage(tg, chatID, tr(lang, "stock_search_started"))

	body, fetchMethod, err := fetcher.Fetch(url)
	if err != nil {
		log.Warn().Err(err).Str("url", url).Msg("zara size lookup: fetch failed, falling back to whole-item stock tracking")
		return false
	}

	variants, err := shops.ParseZaraSizes(body)
	if err != nil {
		log.Info().Err(err).Str("url", url).Msg("zara size lookup: no size data found, falling back to whole-item stock tracking")
		return false
	}

	var outOfStock []shops.ZaraSizeVariant
	for _, v := range variants {
		if !v.InStock {
			outOfStock = append(outOfStock, v)
		}
	}
	if len(outOfStock) == 0 {
		return false
	}

	// fetch_method travels with the candidate list (not the eventual tracker's rule) so
	// createZaraSizeTracker can record how *this* lookup reached the page, once the user
	// picks a size — a re-fetch at that point would risk a different, possibly stale
	// answer, so we don't re-derive it there.
	ruleJSON, err := json.Marshal(zaraSizeSelectionState{FetchMethod: fetchMethod, Variants: outOfStock})
	if err != nil {
		log.Error().Err(err).Msg("failed to marshal zara size candidates")
		return false
	}

	result, _ := extractor.NewGeneric().Extract(body, url)
	title := ""
	if result != nil {
		title = result.Title
	}

	_, err = pool.Exec(ctx, `
		INSERT INTO telegram_states (telegram_id, user_id, step, url, title, rule)
		VALUES ($1, $2, 'awaiting_size', $3, $4, $5)
		ON CONFLICT (telegram_id) DO UPDATE
		SET user_id = $2, step = 'awaiting_size', url = $3, title = $4, rule = $5, updated_at = now()
	`, chatID, userID, url, title, ruleJSON)
	if err != nil {
		log.Error().Err(err).Msg("failed to save size-selection state")
		return false
	}

	var rows [][]inlineButton
	for i, v := range outOfStock {
		rows = append(rows, []inlineButton{button(v.Size, fmt.Sprintf("size:%d", i))})
	}
	rows = append(rows, []inlineButton{button(tr(lang, "button_back"), "menu:back")})
	_ = tg.SendMessageWithMarkup(chatID, tr(lang, "choose_size_prompt"), makeInlineKeyboard(rows...))
	return true
}

// createZaraSizeTracker creates a stock tracker scoped to one size — extraction_rule
// records which one ("zara_size" + size + sku) so the worker's recheck can look up that
// exact variant's availability instead of the item's as a whole.
func createZaraSizeTracker(ctx context.Context, pool *pgxpool.Pool, tg *telegram.Client, chatID int64, userID, lang, url, title, fetchMethod string, variant shops.ZaraSizeVariant, log zerolog.Logger) {
	if !enforceTrackerLimit(ctx, pool, tg, chatID, userID, lang, log) {
		clearTelegramState(ctx, pool, chatID)
		return
	}

	if title == "" {
		title = extractDomain(url)
	}
	rule, _ := json.Marshal(map[string]string{"type": "zara_size", "size": variant.Size, "sku": variant.SKU})
	const stockStatus = "out_of_stock" // only out-of-stock sizes are ever offered

	var trackerID string
	err := pool.QueryRow(ctx, `
		INSERT INTO trackers (user_id, url, normalized_url, domain, title, initial_price, current_price, currency, current_stock_status, tracking_mode, extraction_rule, status, next_check_at)
		VALUES ($1, $2, $2, $3, $4, 0, NULL, 'PLN', $5, 'stock', $6, 'active', now() + interval '3 hours')
		RETURNING id
	`, userID, url, extractDomain(url), title, stockStatus, rule).Scan(&trackerID)
	if err != nil {
		log.Error().Err(err).Msg("failed to create zara size tracker")
		SendTelegramMessage(tg, chatID, tr(lang, "tracker_create_failed"))
		clearTelegramState(ctx, pool, chatID)
		return
	}
	log.Info().
		Int64("telegram_id", chatID).
		Str("user_id", userID).
		Str("tracker_id", trackerID).
		Str("url", url).
		Str("size", variant.Size).
		Msg("telegram zara size tracker created")

	pool.Exec(ctx, `
		INSERT INTO stock_points (id, tracker_id, stock_status, source, status, extraction_method, fetch_method)
		VALUES (gen_random_uuid(), $1, $2, 'bot_add', 'success', 'json_ld', $3)
	`, trackerID, stockStatus, fetchMethod)

	clearTelegramState(ctx, pool, chatID)
	markup := makeInlineKeyboard(
		[]inlineButton{button(tr(lang, "button_trackers"), "menu:list")},
		[]inlineButton{button(tr(lang, "button_back"), "menu:back")},
	)
	_ = tg.SendMessageWithMarkup(chatID, fmt.Sprintf(tr(lang, "size_tracker_created"), title, variant.Size, url, trackerID[:8]), markup)
}

type trackerListRow struct {
	id, url, title, currency, stockStatus, status, trackingMode string
	price                                                       *float64
	interval                                                    int
}

func handleListTrackers(ctx context.Context, pool *pgxpool.Pool, tg *telegram.Client, chatID int64, userID, lang string, log zerolog.Logger) {
	rows, err := pool.Query(ctx, `
		SELECT id, url, COALESCE(title, domain), current_price, currency, current_stock_status, status, check_interval_minutes, tracking_mode
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
		rows.Scan(&t.id, &t.url, &t.title, &t.price, &t.currency, &t.stockStatus, &t.status, &t.interval, &t.trackingMode)
		trackers = append(trackers, t)
	}

	if len(trackers) == 0 {
		sendMainMenu(tg, chatID, lang, tr(lang, "trackers_empty"))
		return
	}

	for i, t := range trackers {
		shortID := t.id[:8]

		card := fmt.Sprintf("🔹 <b>%s</b>", truncate(t.title, 60))
		if t.trackingMode == "stock" {
			if t.stockStatus == "in_stock" {
				card += fmt.Sprintf("\n📦 %s", tr(lang, "tracker_stock_available"))
			} else {
				card += fmt.Sprintf("\n⏳ %s", tr(lang, "tracker_stock_waiting"))
			}
		} else if t.price != nil {
			card += fmt.Sprintf("\n💰 %.2f %s", *t.price, t.currency)
		}
		card += fmt.Sprintf("\n⏱ %s", formatInterval(lang, t.interval))
		card += fmt.Sprintf("\n🔗 <a href=\"%s\">%s</a>", html.EscapeString(t.url), extractDomain(t.url))
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
	fetcher := extractor.NewPageFetcher(rend, cookiesFile, proxyURL, proxypool.NewStore(pool))
	body, fetchMethod, err := fetcher.Fetch(url)
	if err != nil {
		if fallback := extractor.NewSearchFallback(proxypool.NewStore(pool)); fallback != nil {
			notifyStillSearching(tg, chatID, 0, lang)
			if result, fallbackErr := fallback.Extract(nil, url); fallbackErr == nil && len(result.Candidates) > 0 {
				if isSearchFallbackRule(result.Candidates[0].Rule) {
					recordExtractionFailure(ctx, pool, userID, url, fmt.Sprintf("page fetch failed: %s; resolved by %s exact URL fallback", err.Error(), extractor.RuleType(result.Candidates[0].Rule)), log)
				}
				finishAddTracker(ctx, pool, tg, chatID, userID, lang, url, "", result, log)
				return
			}
		}
		recordExtractionFailure(ctx, pool, userID, url, err.Error(), log)
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
		if fallback := extractor.NewSearchFallback(proxypool.NewStore(pool)); fallback != nil {
			notifyStillSearching(tg, chatID, 0, lang)
			result, err = fallback.Extract(body, url)
		}
	}
	if err != nil || len(result.Candidates) == 0 {
		recordExtractionFailure(ctx, pool, userID, url, "no price candidate found", log)
		SendTelegramMessage(tg, chatID, tr(lang, "extract_price_failed"))
		return
	}
	if isSearchFallbackRule(result.Candidates[0].Rule) {
		recordExtractionFailure(ctx, pool, userID, url, fmt.Sprintf("page fetched but direct extractors failed; resolved by %s exact URL fallback", extractor.RuleType(result.Candidates[0].Rule)), log)
	}

	finishAddTracker(ctx, pool, tg, chatID, userID, lang, url, fetchMethod, result, log)
}

// finishAddTracker is the shared tail of handleAddTracker: it persists the first
// candidate from an already-produced ExtractionResult as a new tracker. Shared between
// the normal path (page fetched, ran through attribute-based/generic/search fallback)
// and the fetch-failed path (fetchMethod is "" there — no PageFetcher tier succeeded).
func finishAddTracker(ctx context.Context, pool *pgxpool.Pool, tg *telegram.Client, chatID int64, userID, lang, url, fetchMethod string, result *extractor.ExtractionResult, log zerolog.Logger) {
	candidate := result.Candidates[0]
	newPrice, _, ok := parsePriceInput(candidate.Price)
	if !ok {
		recordExtractionFailure(ctx, pool, userID, url, "failed to parse fallback price: "+candidate.Price, log)
		SendTelegramMessage(tg, chatID, tr(lang, "tracker_create_failed"))
		return
	}

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
		INSERT INTO price_points (id, tracker_id, price, currency, source, status, extraction_method, fetch_method)
		VALUES (gen_random_uuid(), $1, $2, $3, 'bot_add', 'success', $4, NULLIF($5, ''))
	`, trackerID, newPrice, candidate.Currency, extractor.RuleType(candidate.Rule), fetchMethod)

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

// deleteTrackerRow deletes the tracker row and reports whether one was actually found
// and removed, so callers can distinguish "deleted" from "nothing matched" without
// duplicating the query.
func deleteTrackerRow(ctx context.Context, pool *pgxpool.Pool, userID, trackerID string) (bool, error) {
	tag, err := pool.Exec(ctx, `DELETE FROM trackers WHERE id::text LIKE $1 || '%' AND user_id = $2`, trackerID, userID)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

func handleDeleteTracker(ctx context.Context, pool *pgxpool.Pool, tg *telegram.Client, chatID int64, userID, lang, trackerID string, log zerolog.Logger) {
	found, err := deleteTrackerRow(ctx, pool, userID, trackerID)
	if err != nil {
		SendTelegramMessage(tg, chatID, tr(lang, "tracker_delete_failed"))
		return
	}
	if !found {
		SendTelegramMessage(tg, chatID, tr(lang, "tracker_not_found"))
		return
	}
	SendTelegramMessage(tg, chatID, tr(lang, "tracker_deleted"))
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
