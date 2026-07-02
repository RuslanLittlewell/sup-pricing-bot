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
// SerpApi's cold-search fallback can take up to ~45s — long enough that without this
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

// sendTextPriceCandidate is the no-screenshot fallback for sendNextPriceCandidate: it
// runs the same extraction cascade the worker uses (attribute-based → generic → SerpApi) over a
// plain fetch of the page, and if a price comes out, offers it to the user as text — full
// price with currency and the source it was read from — with the usual yes/no
// confirmation. On "yes" the tracker is created with the extractor's rule instead of a
// css_text selector, so periodic checks re-extract the same way. Returns false if no
// price could be extracted (the caller then reports the original failure). statusMsgID,
// when non-zero, is the id of an existing "searching..." message to update in place
// right before the slow SerpApi call, instead of leaving the user staring at a stale
// status with no feedback.
func sendTextPriceCandidate(ctx context.Context, pool *pgxpool.Pool, tg *telegram.Client, chatID int64, userID, lang, url, fallbackCurrency string, log zerolog.Logger, fetcher *extractor.PageFetcher, statusMsgID int) bool {
	if fetcher == nil {
		return false
	}
	body, err := fetcher.Fetch(url)
	if err != nil {
		log.Warn().Err(err).Str("url", url).Msg("text price candidate: fetch failed")
		if serp := extractor.NewSerpAPI(); serp != nil {
			notifyStillSearching(tg, chatID, statusMsgID, lang)
			if result, serpErr := serp.Extract(nil, url); serpErr == nil && len(result.Candidates) > 0 {
				return offerTextPriceCandidate(ctx, pool, tg, chatID, userID, lang, url, fallbackCurrency, result, log)
			}
		}
		return false
	}

	result, err := extractor.NewAttribute().Extract(body, url)
	if err != nil || len(result.Candidates) == 0 {
		result, err = extractor.NewGeneric().Extract(body, url)
	}
	if err != nil || len(result.Candidates) == 0 {
		if serp := extractor.NewSerpAPI(); serp != nil {
			notifyStillSearching(tg, chatID, statusMsgID, lang)
			result, err = serp.Extract(body, url)
		}
	}
	if err != nil || len(result.Candidates) == 0 {
		return false
	}

	return offerTextPriceCandidate(ctx, pool, tg, chatID, userID, lang, url, fallbackCurrency, result, log)
}

// offerTextPriceCandidate saves the first candidate from an already-produced
// ExtractionResult as a pending confirmation and shows it to the user as text. It's the
// shared tail of sendTextPriceCandidate's two paths: the normal one (page fetched, ran
// through attribute-based/generic/SerpApi) and the fetch-failed one (page unreachable, only
// SerpApi's cached rich snippet was available).
func offerTextPriceCandidate(ctx context.Context, pool *pgxpool.Pool, tg *telegram.Client, chatID int64, userID, lang, url, fallbackCurrency string, result *extractor.ExtractionResult, log zerolog.Logger) bool {
	candidate := result.Candidates[0]
	price, _, ok := parsePriceInput(candidate.Price)
	if !ok {
		return false
	}
	currency := candidate.Currency
	if currency == "" {
		currency = fallbackCurrency
	}

	_, err := pool.Exec(ctx, `
		INSERT INTO telegram_states (telegram_id, user_id, step, url, title, initial_price, currency, candidate_index, rule)
		VALUES ($1, $2, 'awaiting_confirm', $3, $7, $4, $5, 0, $6)
		ON CONFLICT (telegram_id) DO UPDATE
		SET user_id = $2, step = 'awaiting_confirm', url = $3, title = $7, initial_price = $4,
		    currency = $5, candidate_index = 0, rule = $6, updated_at = now()
	`, chatID, userID, url, price, currency, candidate.Rule, result.Title)
	if err != nil {
		log.Error().Err(err).Msg("failed to save text price candidate state")
		return false
	}
	log.Info().
		Int64("telegram_id", chatID).
		Str("url", url).
		Float64("price", price).
		Str("currency", currency).
		Str("label", candidate.Label).
		Msg("telegram text price candidate offered")

	markup := makeInlineKeyboard(
		[]inlineButton{button(tr(lang, "button_yes"), "candidate:yes"), button(tr(lang, "button_no"), "candidate:no")},
		[]inlineButton{button(tr(lang, "button_back"), "menu:back")},
	)
	_ = tg.SendMessageWithMarkup(chatID, fmt.Sprintf(tr(lang, "candidate_text_caption"), price, currency), markup)
	return true
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
		// microdata → CSS → SerpApi) may still read a price off the page. Offering that as a
		// text-only candidate lets the user confirm we reached a real price source even
		// when no visual block can be captured. Only on the first attempt: retries with
		// index > 0 would just rediscover the same extraction result in a loop.
		if index == 0 && sendTextPriceCandidate(ctx, pool, tg, chatID, userID, lang, url, currency, log, fetcher, statusMsgID) {
			return
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
