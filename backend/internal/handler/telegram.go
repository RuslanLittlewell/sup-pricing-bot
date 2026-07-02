package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"

	"github.com/littlewell/price-tracker/internal/config"
	"github.com/littlewell/price-tracker/internal/extractor"
	"github.com/littlewell/price-tracker/internal/renderer"
	"github.com/littlewell/price-tracker/internal/telegram"
)

func TelegramWebhook(pool *pgxpool.Pool, cfg *config.Config, tg *telegram.Client, log zerolog.Logger, rend *renderer.Renderer) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var update telegram.Update
		if err := json.NewDecoder(r.Body).Decode(&update); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		// Acknowledge Telegram immediately and flush it over the wire before doing any
		// real work. Without an explicit flush, Go may buffer the response until the
		// handler returns — and price extraction below can take 40+ seconds (SerpApi
		// fallback). If Telegram doesn't see the 200 in time it retries the same update,
		// which is why a single message could otherwise be processed (and answered) twice.
		w.WriteHeader(http.StatusOK)
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}

		if update.CallbackQuery != nil {
			callback := update.CallbackQuery
			_ = tg.AnswerCallbackQuery(callback.ID)

			ctx := context.Background()
			if strings.HasPrefix(callback.Data, "lang:") {
				lang := validBotLanguage(strings.TrimPrefix(callback.Data, "lang:"))
				userID, err := ensureTelegramUser(ctx, pool, callback.From)
				if err != nil {
					log.Error().Err(err).Msg("failed to create telegram user")
					SendTelegramMessage(tg, callback.From.ID, tr(defaultBotLanguage, "account_prepare_failed"))
					return
				}
				if err := setTelegramLanguage(ctx, pool, callback.From.ID, lang); err != nil {
					log.Error().Err(err).Msg("failed to set telegram language")
				}
				chatID := callback.Message.Chat.ID
				if chatID == 0 {
					chatID = callback.From.ID
				}
				clearTelegramState(ctx, pool, chatID)
				_ = userID
				sendMainMenu(tg, chatID, lang, tr(lang, "language_saved"))
				return
			}

			userID, err := getUserIDByTelegramID(ctx, pool, callback.From.ID)
			if err != nil {
				sendLanguageMenu(tg, callback.From.ID)
				return
			}
			lang := getTelegramLanguage(ctx, pool, callback.From.ID)

			chatID := callback.Message.Chat.ID
			if chatID == 0 {
				chatID = callback.From.ID
			}
			log.Info().Int64("user_id", callback.From.ID).Str("data", callback.Data).Msg("telegram callback")
			handleTelegramCallback(ctx, pool, tg, chatID, userID, lang, callback.Data, log, rend, cfg)
			return
		}

		if update.Message == nil {
			return
		}

		text := update.Message.Text
		from := update.Message.From

		log.Info().Int64("user_id", from.ID).Str("text", text).Msg("telegram update")

		ctx := context.Background()
		userID, err := getUserIDByTelegramID(ctx, pool, from.ID)
		if err != nil {
			if strings.HasPrefix(text, "/start") || strings.HasPrefix(text, "/link") {
				handleStartAndLink(pool, tg, from, text, log)
				return
			}
			sendLanguageMenu(tg, from.ID)
			return
		}
		lang := getTelegramLanguage(ctx, pool, from.ID)

		switch {
		case text == "/start":
			clearTelegramState(ctx, pool, from.ID)
			sendLanguageMenu(tg, from.ID)
		case text == "/lang":
			clearTelegramState(ctx, pool, from.ID)
			sendLanguageMenu(tg, from.ID)
		case text == "/help":
			SendTelegramMessage(tg, from.ID, tr(lang, "help"))
		case text == "/list" || strings.EqualFold(strings.TrimSpace(text), "посмотреть мои трекеры") || strings.EqualFold(strings.TrimSpace(text), "my trackers") || strings.EqualFold(strings.TrimSpace(text), "moje trackery"):
			handleListTrackers(ctx, pool, tg, from.ID, userID, lang, log)
		case strings.HasPrefix(text, "/add "):
			handleAddTracker(ctx, pool, tg, from.ID, userID, lang, text[5:], log, rend, cfg.ScraperCookies, cfg.ScraperProxy)
		case strings.HasPrefix(text, "/delete "):
			handleDeleteTracker(ctx, pool, tg, from.ID, userID, lang, text[8:], log)
		case strings.HasPrefix(text, "/check "):
			handleCheckTracker(ctx, pool, tg, from.ID, userID, lang, text[7:], log, rend, cfg.ScraperCookies, cfg.ScraperProxy)
		case strings.HasPrefix(text, "/history "):
			handleTrackerHistory(ctx, pool, tg, from.ID, userID, lang, text[9:], log)
		case handleTrackerDialog(ctx, pool, tg, from.ID, userID, lang, text, log, rend, cfg):
			return
		default:
			SendTelegramMessage(tg, from.ID, tr(lang, "send_link_or_help"))
		}
	}
}

func getUserIDByTelegramID(ctx context.Context, pool *pgxpool.Pool, telegramID int64) (string, error) {
	var userID string
	err := pool.QueryRow(ctx, `SELECT user_id FROM telegram_links WHERE telegram_id = $1`, telegramID).Scan(&userID)
	return userID, err
}

func getTelegramLanguage(ctx context.Context, pool *pgxpool.Pool, telegramID int64) string {
	var lang string
	err := pool.QueryRow(ctx, `SELECT COALESCE(language, 'en') FROM telegram_links WHERE telegram_id = $1`, telegramID).Scan(&lang)
	if err != nil {
		return defaultBotLanguage
	}
	return validBotLanguage(lang)
}

func setTelegramLanguage(ctx context.Context, pool *pgxpool.Pool, telegramID int64, lang string) error {
	_, err := pool.Exec(ctx, `UPDATE telegram_links SET language = $2 WHERE telegram_id = $1`, telegramID, validBotLanguage(lang))
	return err
}

func handleStartAndLink(pool *pgxpool.Pool, tg *telegram.Client, from telegram.User, text string, log zerolog.Logger) {
	if strings.HasPrefix(text, "/link ") || strings.HasPrefix(text, "/start link_") {
		code := ""
		if strings.HasPrefix(text, "/link ") {
			code = strings.TrimSpace(text[6:])
		} else {
			code = strings.TrimSpace(text[12:])
		}

		var userID string
		err := pool.QueryRow(context.Background(), `
			UPDATE telegram_link_codes
			SET expires_at = now() - interval '1 second'
			WHERE code = $1 AND expires_at > now()
			RETURNING user_id
		`, code).Scan(&userID)
		if err != nil {
			SendTelegramMessage(tg, from.ID, tr(defaultBotLanguage, "link_invalid"))
			return
		}

		_, err = pool.Exec(context.Background(), `
			INSERT INTO telegram_links (user_id, telegram_id, telegram_username)
			VALUES ($1, $2, $3)
			ON CONFLICT (user_id) DO UPDATE SET telegram_id = $2, telegram_username = $3
		`, userID, from.ID, from.Username)
		if err != nil {
			log.Error().Err(err).Msg("failed to link telegram")
			SendTelegramMessage(tg, from.ID, tr(defaultBotLanguage, "link_failed"))
			return
		}

		pool.Exec(context.Background(), `DELETE FROM telegram_link_codes WHERE code = $1`, code)
		SendTelegramMessage(tg, from.ID, tr(defaultBotLanguage, "link_success"))
		sendLanguageMenu(tg, from.ID)
		return
	}

	sendLanguageMenu(tg, from.ID)
}

func handleTelegramCallback(ctx context.Context, pool *pgxpool.Pool, tg *telegram.Client, chatID int64, userID, lang, data string, log zerolog.Logger, rend *renderer.Renderer, cfg *config.Config) {
	switch {
	case data == "menu:list":
		clearTelegramState(ctx, pool, chatID)
		handleListTrackers(ctx, pool, tg, chatID, userID, lang, log)
	case data == "menu:back":
		clearTelegramState(ctx, pool, chatID)
		sendMainMenu(tg, chatID, lang, tr(lang, "menu_start"))
	case data == "menu:language":
		clearTelegramState(ctx, pool, chatID)
		sendLanguageMenu(tg, chatID)
	case data == "menu:plans":
		clearTelegramState(ctx, pool, chatID)
		sendPlansMenu(ctx, pool, tg, chatID, userID, lang)
	case strings.HasPrefix(data, "plan:"):
		SendTelegramMessage(tg, chatID, tr(lang, "plan_free_active"))
	case data == "menu:new":
		clearTelegramState(ctx, pool, chatID)
		if !enforceTrackerLimit(ctx, pool, tg, chatID, userID, lang, log) {
			return
		}
		_, err := pool.Exec(ctx, `INSERT INTO telegram_states (telegram_id, user_id, step) VALUES ($1, $2, 'awaiting_mode')`, chatID, userID)
		if err != nil {
			log.Error().Err(err).Msg("failed to start new tracker flow")
			SendTelegramMessage(tg, chatID, tr(lang, "save_link_failed"))
			return
		}
		sendModeMenu(tg, chatID, lang, tr(lang, "choose_tracking_mode"))
	case data == "mode:price":
		state, ok := getTelegramState(ctx, pool, chatID)
		if !ok || state.Step != "awaiting_mode" {
			sendMainMenu(tg, chatID, lang, tr(lang, "menu_stale"))
			return
		}
		if state.URL == "" {
			_, err := pool.Exec(ctx, `UPDATE telegram_states SET step = 'awaiting_url_price', updated_at = now() WHERE telegram_id = $1`, chatID)
			if err != nil {
				log.Error().Err(err).Msg("failed to switch telegram state to awaiting_url_price")
				SendTelegramMessage(tg, chatID, tr(lang, "save_link_failed"))
				return
			}
			sendBackMessage(tg, chatID, lang, tr(lang, "enter_link"))
			return
		}
		_, err := pool.Exec(ctx, `UPDATE telegram_states SET step = 'awaiting_price', updated_at = now() WHERE telegram_id = $1`, chatID)
		if err != nil {
			log.Error().Err(err).Msg("failed to switch telegram state to awaiting_price")
			SendTelegramMessage(tg, chatID, tr(lang, "save_link_failed"))
			return
		}
		sendBackMessage(tg, chatID, lang, tr(lang, "enter_price"))
	case data == "mode:stock":
		state, ok := getTelegramState(ctx, pool, chatID)
		if !ok || state.Step != "awaiting_mode" {
			sendMainMenu(tg, chatID, lang, tr(lang, "menu_stale"))
			return
		}
		if state.URL == "" {
			_, err := pool.Exec(ctx, `UPDATE telegram_states SET step = 'awaiting_url_stock', updated_at = now() WHERE telegram_id = $1`, chatID)
			if err != nil {
				log.Error().Err(err).Msg("failed to switch telegram state to awaiting_url_stock")
				SendTelegramMessage(tg, chatID, tr(lang, "save_link_failed"))
				return
			}
			sendBackMessage(tg, chatID, lang, tr(lang, "enter_link"))
			return
		}
		fetcher := extractor.NewPageFetcher(rend, cfg.ScraperCookies, cfg.ScraperProxy)
		createStockTrackerFromURL(ctx, pool, tg, chatID, userID, lang, state.URL, log, fetcher)
	case data == "candidate:yes":
		state, ok := getTelegramState(ctx, pool, chatID)
		if !ok || state.Step != "awaiting_confirm" {
			sendMainMenu(tg, chatID, lang, tr(lang, "menu_stale"))
			return
		}
		createTrackerFromState(ctx, pool, tg, chatID, userID, lang, state, log)
	case data == "candidate:no":
		state, ok := getTelegramState(ctx, pool, chatID)
		if !ok || state.Step != "awaiting_confirm" {
			sendMainMenu(tg, chatID, lang, tr(lang, "menu_stale"))
			return
		}
		sendNextPriceCandidate(ctx, pool, tg, chatID, userID, lang, state.URL, state.InitialPrice, state.Currency, state.CandidateIndex+1, log, rend,
			extractor.NewPageFetcher(rend, cfg.ScraperCookies, cfg.ScraperProxy))
	case strings.HasPrefix(data, "tracker:delete:"):
		trackerID := strings.TrimPrefix(data, "tracker:delete:")
		handleDeleteTracker(ctx, pool, tg, chatID, userID, lang, trackerID, log)
		handleListTrackers(ctx, pool, tg, chatID, userID, lang, log)
	case strings.HasPrefix(data, "tracker:edit:"):
		trackerID := strings.TrimPrefix(data, "tracker:edit:")
		_, _ = pool.Exec(ctx, `
			INSERT INTO telegram_states (telegram_id, user_id, step, url)
			VALUES ($1, $2, 'awaiting_interval', $3)
			ON CONFLICT (telegram_id) DO UPDATE
			SET user_id = $2, step = 'awaiting_interval', url = $3, updated_at = now()
		`, chatID, userID, trackerID)
		sendIntervalMenu(tg, chatID, lang, trackerID, tr(lang, "interval_prompt"), getPlanLimits(ctx, pool, userID).minIntervalMinutes)
	case strings.HasPrefix(data, "interval:"):
		parts := strings.Split(data, ":")
		if len(parts) != 3 {
			sendMainMenu(tg, chatID, lang, tr(lang, "menu_unknown_interval"))
			return
		}
		minutes, err := strconv.Atoi(parts[2])
		if err != nil {
			sendMainMenu(tg, chatID, lang, tr(lang, "menu_unknown_interval"))
			return
		}
		updateTrackerInterval(ctx, pool, tg, chatID, userID, lang, parts[1], minutes, log)
		clearTelegramState(ctx, pool, chatID)
	default:
		sendMainMenu(tg, chatID, lang, tr(lang, "menu_unknown_button"))
	}
}

func ensureTelegramUser(ctx context.Context, pool *pgxpool.Pool, from telegram.User) (string, error) {
	if userID, err := getUserIDByTelegramID(ctx, pool, from.ID); err == nil {
		return userID, nil
	}

	userID := fmt.Sprintf("tg_%d", from.ID)
	name := from.FirstName
	if name == "" && from.Username != nil {
		name = *from.Username
	}
	email := fmt.Sprintf("%s@telegram.local", userID)

	_, err := pool.Exec(ctx, `
		INSERT INTO "user" (id, name, email, email_verified)
		VALUES ($1, $2, $3, true)
		ON CONFLICT (id) DO UPDATE SET name = EXCLUDED.name
	`, userID, name, email)
	if err != nil {
		return "", err
	}

	_, err = pool.Exec(ctx, `
		INSERT INTO telegram_links (user_id, telegram_id, telegram_username)
		VALUES ($1, $2, $3)
		ON CONFLICT (telegram_id) DO UPDATE SET user_id = $1, telegram_username = $3
	`, userID, from.ID, from.Username)
	if err != nil {
		return "", err
	}

	_, _ = pool.Exec(ctx, `
		INSERT INTO user_plans (user_id, plan_code)
		VALUES ($1, 'free')
		ON CONFLICT (user_id) DO NOTHING
	`, userID)

	return userID, nil
}
