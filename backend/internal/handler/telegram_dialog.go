package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"

	"github.com/littlewell/price-tracker/internal/config"
	"github.com/littlewell/price-tracker/internal/extractor"
	"github.com/littlewell/price-tracker/internal/renderer"
	"github.com/littlewell/price-tracker/internal/telegram"
)

type telegramState struct {
	Step           string
	URL            string
	Title          string
	InitialPrice   float64
	Currency       string
	CandidateIndex int
	Rule           json.RawMessage
}

func handleTrackerDialog(ctx context.Context, pool *pgxpool.Pool, tg *telegram.Client, chatID int64, userID, lang, text string, log zerolog.Logger, rend *renderer.Renderer, cfg *config.Config) bool {
	normalized := strings.TrimSpace(text)
	lowered := strings.ToLower(normalized)

	if lowered == "назад" || lowered == "back" || lowered == "wstecz" {
		clearTelegramState(ctx, pool, chatID)
		sendMainMenu(tg, chatID, lang, tr(lang, "menu_back"))
		return true
	}

	state, hasState := getTelegramState(ctx, pool, chatID)
	if hasState {
		log.Info().
			Int64("telegram_id", chatID).
			Str("user_id", userID).
			Str("step", state.Step).
			Str("url", state.URL).
			Float64("price", state.InitialPrice).
			Str("currency", state.Currency).
			Int("candidate_index", state.CandidateIndex).
			Msg("telegram dialog state loaded")
	}

	if isURL(normalized) {
		if hasState && state.Step == "awaiting_url_price" {
			_, err := pool.Exec(ctx, `UPDATE telegram_states SET step = 'awaiting_price', url = $2, updated_at = now() WHERE telegram_id = $1`, chatID, normalized)
			if err != nil {
				log.Error().Err(err).Msg("failed to save telegram state")
				SendTelegramMessage(tg, chatID, tr(lang, "save_link_failed"))
				return true
			}
			sendBackMessage(tg, chatID, lang, tr(lang, "enter_price"))
			return true
		}
		if hasState && state.Step == "awaiting_url_stock" {
			fetcher := extractor.NewPageFetcher(rend, cfg.ScraperCookies, cfg.ScraperProxy)
			createStockTrackerFromURL(ctx, pool, tg, chatID, userID, lang, normalized, log, fetcher)
			return true
		}
		_, err := pool.Exec(ctx, `
			INSERT INTO telegram_states (telegram_id, user_id, step, url)
			VALUES ($1, $2, 'awaiting_mode', $3)
			ON CONFLICT (telegram_id) DO UPDATE
			SET user_id = $2, step = 'awaiting_mode', url = $3, initial_price = NULL,
			    candidate_index = 0, rule = NULL, updated_at = now()
		`, chatID, userID, normalized)
		if err != nil {
			log.Error().Err(err).Msg("failed to save telegram state")
			SendTelegramMessage(tg, chatID, tr(lang, "save_link_failed"))
			return true
		}
		log.Info().
			Int64("telegram_id", chatID).
			Str("user_id", userID).
			Str("url", normalized).
			Msg("telegram tracker URL received")
		sendModeMenu(tg, chatID, lang, tr(lang, "choose_tracking_mode"))
		return true
	}

	if !hasState {
		return false
	}

	switch state.Step {
	case "awaiting_mode":
		sendModeMenu(tg, chatID, lang, tr(lang, "choose_tracking_mode"))
		return true
	case "awaiting_price":
		price, currency, ok := parsePriceInput(normalized)
		if !ok {
			log.Info().
				Int64("telegram_id", chatID).
				Str("input", normalized).
				Msg("telegram price input rejected")
			sendBackMessage(tg, chatID, lang, tr(lang, "price_invalid"))
			return true
		}
		log.Info().
			Int64("telegram_id", chatID).
			Str("url", state.URL).
			Float64("price", price).
			Str("currency", currency).
			Msg("telegram price input accepted")
		sendNextPriceCandidate(ctx, pool, tg, chatID, userID, lang, state.URL, price, currency, 0, log, rend,
			extractor.NewPageFetcher(rend, cfg.ScraperCookies, cfg.ScraperProxy))
		return true
	case "awaiting_confirm":
		switch lowered {
		case "да", "yes", "y", "+", "ок", "ok", "tak":
			log.Info().
				Int64("telegram_id", chatID).
				Str("url", state.URL).
				Float64("price", state.InitialPrice).
				Int("candidate_index", state.CandidateIndex).
				Msg("telegram price candidate accepted")
			if len(state.Rule) == 0 {
				SendTelegramMessage(tg, chatID, tr(lang, "save_candidate_failed"))
				clearTelegramState(ctx, pool, chatID)
				return true
			}
			createTrackerFromState(ctx, pool, tg, chatID, userID, lang, state, log)
			return true
		case "нет", "no", "n", "-", "дальше", "nie":
			log.Info().
				Int64("telegram_id", chatID).
				Str("url", state.URL).
				Float64("price", state.InitialPrice).
				Int("candidate_index", state.CandidateIndex).
				Msg("telegram price candidate rejected")
			sendNextPriceCandidate(ctx, pool, tg, chatID, userID, lang, state.URL, state.InitialPrice, state.Currency, state.CandidateIndex+1, log, rend,
				extractor.NewPageFetcher(rend, cfg.ScraperCookies, cfg.ScraperProxy))
			return true
		default:
			markup := makeInlineKeyboard(
				[]inlineButton{button(tr(lang, "button_yes"), "candidate:yes"), button(tr(lang, "button_no"), "candidate:no")},
				[]inlineButton{button(tr(lang, "button_back"), "menu:back")},
			)
			_ = tg.SendMessageWithMarkup(chatID, tr(lang, "confirm_prompt"), markup)
			return true
		}
	case "awaiting_interval":
		interval, ok := parseIntervalMinutes(normalized)
		if !ok {
			sendIntervalMenu(tg, chatID, lang, state.URL, tr(lang, "interval_invalid"), getPlanLimits(ctx, pool, userID).minIntervalMinutes)
			return true
		}
		updateTrackerInterval(ctx, pool, tg, chatID, userID, lang, state.URL, interval, log)
		clearTelegramState(ctx, pool, chatID)
		return true
	}

	return false
}

// notifyStillSearching lets the user know a price search is taking longer than usual.
// Search fallback calls can take up to ~45s — long enough that without this
// notice a user might think the bot stalled. It edits statusMsgID in place when one is
// available (so we don't spam a new message for every progress update); otherwise it
// sends a new message, since some call sites (e.g. /add, /check) never show an initial
// status message at all.
func notifyStillSearching(tg *telegram.Client, chatID int64, statusMsgID int, lang string) {
	text := tr(lang, "search_more_time")
	if statusMsgID != 0 {
		if err := tg.EditMessageText(chatID, statusMsgID, text); err == nil {
			return
		}
	}
	SendTelegramMessage(tg, chatID, text)
}

// sendTextPriceCandidate is the no-screenshot fallback for sendNextPriceCandidate. It
// creates a tracker only when the fallback found the same requested URL and the exact
// price the user entered. Ambiguous search results are logged and declined without
// asking the user to approve a potentially unrelated price.
func sendTextPriceCandidate(ctx context.Context, pool *pgxpool.Pool, tg *telegram.Client, chatID int64, userID, lang, url string, expectedPrice float64, fallbackCurrency string, log zerolog.Logger, fetcher *extractor.PageFetcher, statusMsgID int) bool {
	if fetcher == nil {
		return false
	}
	body, err := fetcher.Fetch(url)
	if err != nil {
		log.Warn().Err(err).Str("url", url).Msg("text price candidate: fetch failed")
		if fallback := extractor.NewSearchFallback(); fallback != nil {
			notifyStillSearching(tg, chatID, statusMsgID, lang)
			if result, fallbackErr := fallback.Extract(nil, url); fallbackErr == nil && len(result.Candidates) > 0 {
				return handleTextPriceCandidate(ctx, pool, tg, chatID, userID, lang, url, expectedPrice, fallbackCurrency, result, "page fetch failed: "+err.Error(), log)
			}
		}
		return false
	}

	result, err := extractor.NewAttribute().Extract(body, url)
	if err != nil || len(result.Candidates) == 0 {
		result, err = extractor.NewGeneric().Extract(body, url)
	}
	if err != nil || len(result.Candidates) == 0 {
		if fallback := extractor.NewSearchFallback(); fallback != nil {
			notifyStillSearching(tg, chatID, statusMsgID, lang)
			result, err = fallback.Extract(body, url)
		}
	}
	if err != nil || len(result.Candidates) == 0 {
		return false
	}

	return handleTextPriceCandidate(ctx, pool, tg, chatID, userID, lang, url, expectedPrice, fallbackCurrency, result, "screenshot price block not found", log)
}

func handleTextPriceCandidate(ctx context.Context, pool *pgxpool.Pool, tg *telegram.Client, chatID int64, userID, lang, url string, expectedPrice float64, fallbackCurrency string, result *extractor.ExtractionResult, directErr string, log zerolog.Logger) bool {
	// Some extractors return multiple readings of the same underlying value (e.g.
	// a raw JSON price field read both literally and as minor units) since we
	// can't tell upfront which one is correct — so every candidate needs to be
	// checked against the expected price, not just the first.
	for _, candidate := range result.Candidates {
		price, _, ok := parsePriceInput(candidate.Price)
		if !ok {
			continue
		}
		currency := candidate.Currency
		if currency == "" {
			currency = fallbackCurrency
		}

		sourceMatches := candidate.SourceURL == "" || extractor.SameURL(candidate.SourceURL, url)
		priceMatches := priceCents(price) == priceCents(expectedPrice)
		if sourceMatches && priceMatches {
			if isSearchFallbackRule(candidate.Rule) {
				recordExtractionFailure(ctx, pool, userID, url, fmt.Sprintf("%s; resolved by %s exact URL+price fallback", directErr, extractor.RuleType(candidate.Rule)), log)
			}
			createTrackerFromState(ctx, pool, tg, chatID, userID, lang, telegramState{
				URL:          url,
				Title:        result.Title,
				InitialPrice: price,
				Currency:     currency,
				Rule:         candidate.Rule,
			}, log)
			return true
		}
	}

	candidate := result.Candidates[0]
	price, _, _ := parsePriceInput(candidate.Price)
	currency := candidate.Currency
	if currency == "" {
		currency = fallbackCurrency
	}
	errMsg := fmt.Sprintf("%s; fallback was not exact enough: expected=%.2f %s candidate=%s %s source=%q method=%s", directErr, expectedPrice, fallbackCurrency, candidate.Price, currency, candidate.SourceURL, extractor.RuleType(candidate.Rule))
	recordExtractionFailure(ctx, pool, userID, url, errMsg, log)
	log.Info().
		Int64("telegram_id", chatID).
		Str("url", url).
		Float64("expected_price", expectedPrice).
		Float64("candidate_price", price).
		Str("currency", currency).
		Str("label", candidate.Label).
		Str("source_url", candidate.SourceURL).
		Msg("telegram text price candidate rejected as inexact")

	SendTelegramMessage(tg, chatID, tr(lang, "protected_site_noted"))
	clearTelegramState(ctx, pool, chatID)
	return true
}

func isSearchFallbackRule(rule json.RawMessage) bool {
	switch extractor.RuleType(rule) {
	case "openserp_search_result", "openserp_extract", "serper_organic_result", "serper_shopping_result", "serpapi_rich_snippet":
		return true
	default:
		return false
	}
}

func priceCents(price float64) int64 {
	return int64(price*100 + 0.5)
}

func getTelegramState(ctx context.Context, pool *pgxpool.Pool, telegramID int64) (telegramState, bool) {
	var state telegramState
	err := pool.QueryRow(ctx, `
		SELECT step, COALESCE(url, ''), COALESCE(title, ''), COALESCE(initial_price, 0), COALESCE(currency, 'PLN'), candidate_index, COALESCE(rule, '{}'::jsonb)
		FROM telegram_states WHERE telegram_id = $1
	`, telegramID).Scan(&state.Step, &state.URL, &state.Title, &state.InitialPrice, &state.Currency, &state.CandidateIndex, &state.Rule)
	return state, err == nil
}

func clearTelegramState(ctx context.Context, pool *pgxpool.Pool, telegramID int64) {
	_, _ = pool.Exec(ctx, `DELETE FROM telegram_states WHERE telegram_id = $1`, telegramID)
}

func sendNextPriceCandidate(ctx context.Context, pool *pgxpool.Pool, tg *telegram.Client, chatID int64, userID, lang, url string, price float64, currency string, index int, log zerolog.Logger, rend *renderer.Renderer, fetcher *extractor.PageFetcher) {
	if rend == nil {
		SendTelegramMessage(tg, chatID, tr(lang, "search_unavailable"))
		clearTelegramState(ctx, pool, chatID)
		return
	}

	statusMsgID, err := tg.SendMessageGetID(chatID, tr(lang, "search_started"))
	if err != nil {
		log.Warn().Err(err).Msg("failed to send search-started message")
	}
	log.Info().
		Int64("telegram_id", chatID).
		Str("user_id", userID).
		Str("url", url).
		Float64("price", price).
		Str("currency", currency).
		Int("candidate_index", index).
		Msg("telegram price block search started")

	candidate, screenshot, err := rend.FindPriceBlock(ctx, url, formatPriceForSearch(price), index)
	if err != nil {
		log.Error().Err(err).Str("url", url).Float64("price", price).Int("index", index).Msg("failed to find price block")
		// The screenshot path failed, but the extraction pipeline (JSON-LD → meta →
		// microdata → CSS → search fallback) may still read a price off the page. Offering that as a
		// text-only candidate lets the user confirm we reached a real price source even
		// when no visual block can be captured. Only on the first attempt: retries with
		// index > 0 would just rediscover the same extraction result in a loop.
		if index == 0 && sendTextPriceCandidate(ctx, pool, tg, chatID, userID, lang, url, price, currency, log, fetcher, statusMsgID) {
			return
		}
		// Only log once we've actually given up (index == 0, the only attempt for
		// "access denied"/"price not found"; the last retry for "no more
		// candidates") — not on every intermediate retry for the same URL.
		if index == 0 {
			recordExtractionFailure(ctx, pool, userID, url, err.Error(), log)
		}
		errText := strings.ToLower(err.Error())
		if strings.Contains(errText, "access denied") || strings.Contains(errText, "permission to access") {
			SendTelegramMessage(tg, chatID, tr(lang, "access_denied"))
		} else if index == 0 {
			SendTelegramMessage(tg, chatID, tr(lang, "price_not_found"))
		} else {
			SendTelegramMessage(tg, chatID, tr(lang, "no_more_candidates"))
		}
		clearTelegramState(ctx, pool, chatID)
		return
	}
	log.Info().
		Int64("telegram_id", chatID).
		Str("url", url).
		Str("selector", candidate.Selector).
		Str("screenshot_selector", candidate.ScreenshotSelector).
		Str("price_text", truncate(candidate.PriceText, 180)).
		Int("price_token_index", candidate.PriceTokenIndex).
		Str("text", truncate(candidate.Text, 180)).
		Int("candidate_index", index).
		Int("total_found", candidate.TotalFound).
		Int("screenshot_bytes", len(screenshot)).
		Msg("telegram price block candidate found")

	rule, _ := json.Marshal(map[string]interface{}{
		"type":                "css_text",
		"selector":            candidate.Selector,
		"screenshot_selector": candidate.ScreenshotSelector,
		"price_text":          candidate.PriceText,
		"price_token_index":   candidate.PriceTokenIndex,
	})

	_, err = pool.Exec(ctx, `
		INSERT INTO telegram_states (telegram_id, user_id, step, url, title, initial_price, currency, candidate_index, rule)
		VALUES ($1, $2, 'awaiting_confirm', $3, $8, $4, $5, $6, $7)
		ON CONFLICT (telegram_id) DO UPDATE
		SET user_id = $2, step = 'awaiting_confirm', url = $3, title = $8, initial_price = $4,
		    currency = $5, candidate_index = $6, rule = $7, updated_at = now()
	`, chatID, userID, url, price, currency, index, rule, candidate.Title)
	if err != nil {
		log.Error().Err(err).Msg("failed to save price candidate state")
		SendTelegramMessage(tg, chatID, tr(lang, "save_candidate_failed"))
		return
	}
	log.Info().
		Int64("telegram_id", chatID).
		Str("url", url).
		Str("selector", candidate.Selector).
		Str("price_text", truncate(candidate.PriceText, 180)).
		Int("price_token_index", candidate.PriceTokenIndex).
		Int("candidate_index", index).
		Msg("telegram price candidate state saved")

	markup := makeInlineKeyboard(
		[]inlineButton{button(tr(lang, "button_yes"), "candidate:yes"), button(tr(lang, "button_no"), "candidate:no")},
		[]inlineButton{button(tr(lang, "button_back"), "menu:back")},
	)
	caption := fmt.Sprintf(tr(lang, "candidate_caption"), formatMoney(price))
	if err := tg.SendPhotoWithMarkup(chatID, screenshot, caption, markup); err != nil {
		log.Error().Err(err).Msg("failed to send price screenshot")
		if docErr := tg.SendDocumentWithMarkup(chatID, screenshot, caption, markup); docErr != nil {
			log.Error().Err(docErr).Msg("failed to send price screenshot as document")
			_ = tg.SendMessageWithMarkup(chatID, caption, markup)
			return
		}
		log.Info().
			Int64("telegram_id", chatID).
			Str("url", url).
			Int("candidate_index", index).
			Int("screenshot_bytes", len(screenshot)).
			Msg("telegram price screenshot sent as document")
		return
	}
	log.Info().
		Int64("telegram_id", chatID).
		Str("url", url).
		Int("candidate_index", index).
		Int("screenshot_bytes", len(screenshot)).
		Msg("telegram price screenshot sent")
}
