package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
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
		w.WriteHeader(http.StatusOK)

		if update.CallbackQuery != nil {
			callback := update.CallbackQuery
			_ = tg.AnswerCallbackQuery(callback.ID)

			ctx := context.Background()
			userID, err := getUserIDByTelegramID(ctx, pool, callback.From.ID)
			if err != nil {
				SendTelegramMessage(tg, callback.From.ID, "Аккаунт не привязан. Нажмите /start.")
				return
			}

			chatID := callback.Message.Chat.ID
			if chatID == 0 {
				chatID = callback.From.ID
			}
			log.Info().Int64("user_id", callback.From.ID).Str("data", callback.Data).Msg("telegram callback")
			handleTelegramCallback(ctx, pool, tg, chatID, userID, callback.Data, log, rend)
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
			SendTelegramMessage(tg, from.ID, "Аккаунт не привязан. Сначала привяжите Telegram на сайте: Профиль → Привязать Telegram.")
			return
		}

		switch {
		case text == "/start":
			clearTelegramState(ctx, pool, from.ID)
			sendMainMenu(tg, from.ID, "Введите ссылку товара для трекинга")
		case text == "/help":
			SendTelegramMessage(tg, from.ID, "Отправьте ссылку товара, затем текущую цену. Команды:\n/list — список трекеров\n/delete <id> — удалить\n/check <id> — проверить сейчас\n/history <id> — история изменений")
		case text == "/list" || strings.EqualFold(strings.TrimSpace(text), "посмотреть мои трекеры"):
			handleListTrackers(ctx, pool, tg, from.ID, userID, log)
		case strings.HasPrefix(text, "/add "):
			handleAddTracker(ctx, pool, tg, from.ID, userID, text[5:], log, rend, cfg.ScraperCookies)
		case strings.HasPrefix(text, "/delete "):
			handleDeleteTracker(ctx, pool, tg, from.ID, userID, text[8:], log)
		case strings.HasPrefix(text, "/check "):
			handleCheckTracker(ctx, pool, tg, from.ID, userID, text[7:], log, rend, cfg.ScraperCookies)
		case strings.HasPrefix(text, "/history "):
			handleTrackerHistory(ctx, pool, tg, from.ID, userID, text[9:], log)
		case handleTrackerDialog(ctx, pool, tg, from.ID, userID, text, log, rend):
			return
		default:
			SendTelegramMessage(tg, from.ID, "Отправьте ссылку товара для трекинга или /help.")
		}
	}
}

func getUserIDByTelegramID(ctx context.Context, pool *pgxpool.Pool, telegramID int64) (string, error) {
	var userID string
	err := pool.QueryRow(ctx, `SELECT user_id FROM telegram_links WHERE telegram_id = $1`, telegramID).Scan(&userID)
	return userID, err
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
			SendTelegramMessage(tg, from.ID, "Код недействителен или истёк. Запросите новый на сайте.")
			return
		}

		_, err = pool.Exec(context.Background(), `
			INSERT INTO telegram_links (user_id, telegram_id, telegram_username)
			VALUES ($1, $2, $3)
			ON CONFLICT (user_id) DO UPDATE SET telegram_id = $2, telegram_username = $3
		`, userID, from.ID, from.Username)
		if err != nil {
			log.Error().Err(err).Msg("failed to link telegram")
			SendTelegramMessage(tg, from.ID, "Ошибка при привязке. Попробуйте снова.")
			return
		}

		pool.Exec(context.Background(), `DELETE FROM telegram_link_codes WHERE code = $1`, code)
		SendTelegramMessage(tg, from.ID, "✅ Telegram привязан! Теперь вы можете управлять трекерами.")
		return
	}

	userID, err := ensureTelegramUser(context.Background(), pool, from)
	if err != nil {
		log.Error().Err(err).Msg("failed to create telegram user")
		SendTelegramMessage(tg, from.ID, "Не удалось подготовить аккаунт. Попробуйте позже.")
		return
	}
	_ = userID
	sendMainMenu(tg, from.ID, "Введите ссылку товара для трекинга")
}

type inlineButton struct {
	Text         string `json:"text"`
	CallbackData string `json:"callback_data"`
}

type inlineKeyboard struct {
	InlineKeyboard [][]inlineButton `json:"inline_keyboard"`
}

func makeInlineKeyboard(rows ...[]inlineButton) json.RawMessage {
	data, _ := json.Marshal(inlineKeyboard{InlineKeyboard: rows})
	return data
}

func button(text, data string) inlineButton {
	return inlineButton{Text: text, CallbackData: data}
}

func sendMainMenu(tg *telegram.Client, chatID int64, text string) {
	markup := makeInlineKeyboard(
		[]inlineButton{button("Посмотреть мои трекеры", "menu:list")},
	)
	_ = tg.SendMessageWithMarkup(chatID, text, markup)
}

func sendBackMessage(tg *telegram.Client, chatID int64, text string) {
	markup := makeInlineKeyboard([]inlineButton{button("Назад", "menu:back")})
	_ = tg.SendMessageWithMarkup(chatID, text, markup)
}

func handleTelegramCallback(ctx context.Context, pool *pgxpool.Pool, tg *telegram.Client, chatID int64, userID, data string, log zerolog.Logger, rend *renderer.Renderer) {
	switch {
	case data == "menu:list":
		clearTelegramState(ctx, pool, chatID)
		handleListTrackers(ctx, pool, tg, chatID, userID, log)
	case data == "menu:back":
		clearTelegramState(ctx, pool, chatID)
		sendMainMenu(tg, chatID, "Введите ссылку товара для трекинга")
	case data == "candidate:yes":
		state, ok := getTelegramState(ctx, pool, chatID)
		if !ok || state.Step != "awaiting_confirm" {
			sendMainMenu(tg, chatID, "Этот выбор уже не актуален.")
			return
		}
		createTrackerFromState(ctx, pool, tg, chatID, userID, state, log)
	case data == "candidate:no":
		state, ok := getTelegramState(ctx, pool, chatID)
		if !ok || state.Step != "awaiting_confirm" {
			sendMainMenu(tg, chatID, "Этот выбор уже не актуален.")
			return
		}
		sendNextPriceCandidate(ctx, pool, tg, chatID, userID, state.URL, state.InitialPrice, state.Currency, state.CandidateIndex+1, log, rend)
	case strings.HasPrefix(data, "tracker:delete:"):
		trackerID := strings.TrimPrefix(data, "tracker:delete:")
		handleDeleteTracker(ctx, pool, tg, chatID, userID, trackerID, log)
		handleListTrackers(ctx, pool, tg, chatID, userID, log)
	case strings.HasPrefix(data, "tracker:edit:"):
		trackerID := strings.TrimPrefix(data, "tracker:edit:")
		_, _ = pool.Exec(ctx, `
			INSERT INTO telegram_states (telegram_id, user_id, step, url)
			VALUES ($1, $2, 'awaiting_interval', $3)
			ON CONFLICT (telegram_id) DO UPDATE
			SET user_id = $2, step = 'awaiting_interval', url = $3, updated_at = now()
		`, chatID, userID, trackerID)
		sendIntervalMenu(tg, chatID, trackerID, "Выберите интенсивность сканирования")
	case strings.HasPrefix(data, "interval:"):
		parts := strings.Split(data, ":")
		if len(parts) != 3 {
			sendMainMenu(tg, chatID, "Не понял выбранный интервал.")
			return
		}
		minutes, err := strconv.Atoi(parts[2])
		if err != nil {
			sendMainMenu(tg, chatID, "Не понял выбранный интервал.")
			return
		}
		updateTrackerInterval(ctx, pool, tg, chatID, userID, parts[1], minutes, log)
		clearTelegramState(ctx, pool, chatID)
	default:
		sendMainMenu(tg, chatID, "Не понял кнопку.")
	}
}

func sendIntervalMenu(tg *telegram.Client, chatID int64, trackerID, text string) {
	markup := makeInlineKeyboard(
		[]inlineButton{button("1ч", "interval:"+trackerID+":60"), button("3ч", "interval:"+trackerID+":180")},
		[]inlineButton{button("5ч", "interval:"+trackerID+":300"), button("24ч", "interval:"+trackerID+":1440")},
		[]inlineButton{button("Назад", "menu:list")},
	)
	_ = tg.SendMessageWithMarkup(chatID, text, markup)
}

func updateTrackerInterval(ctx context.Context, pool *pgxpool.Pool, tg *telegram.Client, chatID int64, userID, trackerID string, minutes int, log zerolog.Logger) {
	if minutes < 15 {
		minutes = 15
	}
	if minutes > 1440 {
		minutes = 1440
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
		SendTelegramMessage(tg, chatID, "Ошибка при сохранении интенсивности.")
		return
	}
	if tag.RowsAffected() == 0 {
		SendTelegramMessage(tg, chatID, "Трекер не найден.")
		return
	}
	markup := makeInlineKeyboard(
		[]inlineButton{button("Посмотреть мои трекеры", "menu:list")},
		[]inlineButton{button("Назад", "menu:list")},
	)
	_ = tg.SendMessageWithMarkup(chatID, "✅ Интенсивность обновлена: "+formatInterval(minutes), markup)
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

type telegramState struct {
	Step           string
	URL            string
	InitialPrice   float64
	Currency       string
	CandidateIndex int
	Rule           json.RawMessage
}

func handleTrackerDialog(ctx context.Context, pool *pgxpool.Pool, tg *telegram.Client, chatID int64, userID, text string, log zerolog.Logger, rend *renderer.Renderer) bool {
	normalized := strings.TrimSpace(text)
	lowered := strings.ToLower(normalized)

	if lowered == "назад" || lowered == "back" {
		clearTelegramState(ctx, pool, chatID)
		sendMainMenu(tg, chatID, "Окей, вернулись назад.")
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
		_, err := pool.Exec(ctx, `
			INSERT INTO telegram_states (telegram_id, user_id, step, url)
			VALUES ($1, $2, 'awaiting_price', $3)
			ON CONFLICT (telegram_id) DO UPDATE
			SET user_id = $2, step = 'awaiting_price', url = $3, initial_price = NULL,
			    candidate_index = 0, rule = NULL, updated_at = now()
		`, chatID, userID, normalized)
		if err != nil {
			log.Error().Err(err).Msg("failed to save telegram state")
			SendTelegramMessage(tg, chatID, "Не удалось сохранить ссылку. Попробуйте ещё раз.")
			return true
		}
		log.Info().
			Int64("telegram_id", chatID).
			Str("user_id", userID).
			Str("url", normalized).
			Msg("telegram tracker URL received")
		sendBackMessage(tg, chatID, "Введите текущую цену")
		return true
	}

	if !hasState {
		return false
	}

	switch state.Step {
	case "awaiting_price":
		price, currency, ok := parsePriceInput(normalized)
		if !ok {
			log.Info().
				Int64("telegram_id", chatID).
				Str("input", normalized).
				Msg("telegram price input rejected")
			sendBackMessage(tg, chatID, "Введите цену числом, например: 1299, 1299.99 или 1299 zł")
			return true
		}
		log.Info().
			Int64("telegram_id", chatID).
			Str("url", state.URL).
			Float64("price", price).
			Str("currency", currency).
			Msg("telegram price input accepted")
		sendNextPriceCandidate(ctx, pool, tg, chatID, userID, state.URL, price, currency, 0, log, rend)
		return true
	case "awaiting_confirm":
		switch lowered {
		case "да", "yes", "y", "+", "ок", "ok":
			log.Info().
				Int64("telegram_id", chatID).
				Str("url", state.URL).
				Float64("price", state.InitialPrice).
				Int("candidate_index", state.CandidateIndex).
				Msg("telegram price candidate accepted")
			if len(state.Rule) == 0 {
				SendTelegramMessage(tg, chatID, "Не вижу выбранный блок. Отправьте ссылку заново.")
				clearTelegramState(ctx, pool, chatID)
				return true
			}
			createTrackerFromState(ctx, pool, tg, chatID, userID, state, log)
			return true
		case "нет", "no", "n", "-", "дальше":
			log.Info().
				Int64("telegram_id", chatID).
				Str("url", state.URL).
				Float64("price", state.InitialPrice).
				Int("candidate_index", state.CandidateIndex).
				Msg("telegram price candidate rejected")
			sendNextPriceCandidate(ctx, pool, tg, chatID, userID, state.URL, state.InitialPrice, state.Currency, state.CandidateIndex+1, log, rend)
			return true
		default:
			markup := makeInlineKeyboard(
				[]inlineButton{button("Да", "candidate:yes"), button("Нет", "candidate:no")},
				[]inlineButton{button("Назад", "menu:back")},
			)
			_ = tg.SendMessageWithMarkup(chatID, "Ответьте «да», если блок с ценой найден верно, или «нет», чтобы попробовать следующий.", markup)
			return true
		}
	case "awaiting_interval":
		interval, ok := parseIntervalMinutes(normalized)
		if !ok {
			sendIntervalMenu(tg, chatID, state.URL, "Выберите интенсивность или введите количество минут числом.")
			return true
		}
		updateTrackerInterval(ctx, pool, tg, chatID, userID, state.URL, interval, log)
		clearTelegramState(ctx, pool, chatID)
		return true
	}

	return false
}

func getTelegramState(ctx context.Context, pool *pgxpool.Pool, telegramID int64) (telegramState, bool) {
	var state telegramState
	err := pool.QueryRow(ctx, `
		SELECT step, COALESCE(url, ''), COALESCE(initial_price, 0), COALESCE(currency, 'PLN'), candidate_index, COALESCE(rule, '{}'::jsonb)
		FROM telegram_states WHERE telegram_id = $1
	`, telegramID).Scan(&state.Step, &state.URL, &state.InitialPrice, &state.Currency, &state.CandidateIndex, &state.Rule)
	return state, err == nil
}

func sendNextPriceCandidate(ctx context.Context, pool *pgxpool.Pool, tg *telegram.Client, chatID int64, userID, url string, price float64, currency string, index int, log zerolog.Logger, rend *renderer.Renderer) {
	if rend == nil {
		SendTelegramMessage(tg, chatID, "Не могу продолжить поиск прямо сейчас. Отправьте ссылку заново.")
		clearTelegramState(ctx, pool, chatID)
		return
	}

	SendTelegramMessage(tg, chatID, "Ищу блок с ценой на странице...")
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
		errText := strings.ToLower(err.Error())
		if strings.Contains(errText, "access denied") || strings.Contains(errText, "permission to access") {
			SendTelegramMessage(tg, chatID, "Сайт заблокировал загрузку страницы для бота (Access Denied). Для этой ссылки пока не могу снять скриншот цены.")
		} else if index == 0 {
			SendTelegramMessage(tg, chatID, "Не нашёл цену на странице. Проверьте ссылку и цену, затем отправьте ссылку заново.")
		} else {
			SendTelegramMessage(tg, chatID, "Других блоков с этой ценой не нашёл. Отправьте ссылку заново или другую цену.")
		}
		clearTelegramState(ctx, pool, chatID)
		return
	}
	log.Info().
		Int64("telegram_id", chatID).
		Str("url", url).
		Str("selector", candidate.Selector).
		Str("text", truncate(candidate.Text, 180)).
		Int("candidate_index", index).
		Int("total_found", candidate.TotalFound).
		Int("screenshot_bytes", len(screenshot)).
		Msg("telegram price block candidate found")

	rule, _ := json.Marshal(map[string]string{
		"type":     "css_text",
		"selector": candidate.Selector,
	})

	_, err = pool.Exec(ctx, `
		INSERT INTO telegram_states (telegram_id, user_id, step, url, initial_price, currency, candidate_index, rule)
		VALUES ($1, $2, 'awaiting_confirm', $3, $4, $5, $6, $7)
		ON CONFLICT (telegram_id) DO UPDATE
		SET user_id = $2, step = 'awaiting_confirm', url = $3, initial_price = $4,
		    currency = $5, candidate_index = $6, rule = $7, updated_at = now()
	`, chatID, userID, url, price, currency, index, rule)
	if err != nil {
		log.Error().Err(err).Msg("failed to save price candidate state")
		SendTelegramMessage(tg, chatID, "Не удалось сохранить найденный блок. Попробуйте заново.")
		return
	}
	log.Info().
		Int64("telegram_id", chatID).
		Str("url", url).
		Str("selector", candidate.Selector).
		Int("candidate_index", index).
		Msg("telegram price candidate state saved")

	markup := makeInlineKeyboard(
		[]inlineButton{button("Да", "candidate:yes"), button("Нет", "candidate:no")},
		[]inlineButton{button("Назад", "menu:back")},
	)
	caption := fmt.Sprintf("Нашёл этот блок с ценой %s.\nЭто правильная цена?", formatMoney(price))
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

func createTrackerFromState(ctx context.Context, pool *pgxpool.Pool, tg *telegram.Client, chatID int64, userID string, state telegramState, log zerolog.Logger) {
	var trackerID string
	err := pool.QueryRow(ctx, `
		INSERT INTO trackers (user_id, url, normalized_url, domain, title, initial_price, current_price, currency, current_stock_status, extraction_rule, extraction_confidence, status, next_check_at)
		VALUES ($1, $2, $2, $3, $3, $4, $4, $5, 'unknown', $6, 0.9, 'active', now() + interval '3 hours')
		RETURNING id
	`, userID, state.URL, extractDomain(state.URL), state.InitialPrice, state.Currency, state.Rule).Scan(&trackerID)
	if err != nil {
		log.Error().Err(err).Msg("failed to create tracker from telegram state")
		SendTelegramMessage(tg, chatID, "Ошибка при создании трекера.")
		return
	}
	log.Info().
		Int64("telegram_id", chatID).
		Str("user_id", userID).
		Str("tracker_id", trackerID).
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
		[]inlineButton{button("Посмотреть мои трекеры", "menu:list")},
		[]inlineButton{button("Назад", "menu:back")},
	)
	_ = tg.SendMessageWithMarkup(chatID, fmt.Sprintf("✅ Трекер создан!\n\n%s\n%s\nПроверка каждые 3 часа.\nID: <code>%s</code>", state.URL, formatMoney(state.InitialPrice), trackerID[:8]), markup)
}

func clearTelegramState(ctx context.Context, pool *pgxpool.Pool, telegramID int64) {
	_, _ = pool.Exec(ctx, `DELETE FROM telegram_states WHERE telegram_id = $1`, telegramID)
}

func handleListTrackers(ctx context.Context, pool *pgxpool.Pool, tg *telegram.Client, chatID int64, userID string, log zerolog.Logger) {
	rows, err := pool.Query(ctx, `
		SELECT id, url, COALESCE(title, domain), current_price, currency, current_stock_status, status, check_interval_minutes
		FROM trackers WHERE user_id = $1 AND status != 'deleted' ORDER BY created_at DESC
	`, userID)
	if err != nil {
		SendTelegramMessage(tg, chatID, "Ошибка загрузки трекеров.")
		return
	}
	defer rows.Close()

	var lines []string
	var rowsMarkup [][]inlineButton
	for rows.Next() {
		var id, url, title, currency, stockStatus, status string
		var price *float64
		var interval int
		rows.Scan(&id, &url, &title, &price, &currency, &stockStatus, &status, &interval)

		line := fmt.Sprintf("🔹 <b>%s</b>", truncate(title, 30))
		if price != nil {
			line += "\n   " + formatMoney(*price)
		}
		line += fmt.Sprintf("\n   📦 %s | %s", stockStatus, status)
		line += fmt.Sprintf("\n   ⏱ %s", formatInterval(interval))
		line += fmt.Sprintf("\n   ID: <code>%s</code>", id[:8])
		lines = append(lines, line)
		shortID := id[:8]
		rowsMarkup = append(rowsMarkup,
			[]inlineButton{button("Редактировать", "tracker:edit:"+shortID), button("Удалить", "tracker:delete:"+shortID)},
		)
	}

	if len(lines) == 0 {
		sendMainMenu(tg, chatID, "У вас нет трекеров. Отправьте ссылку товара, чтобы добавить первый.")
		return
	}

	msg := strings.Join(lines, "\n\n")
	rowsMarkup = append(rowsMarkup, []inlineButton{button("Назад", "menu:back")})
	_ = tg.SendMessageWithMarkup(chatID, msg, makeInlineKeyboard(rowsMarkup...))
}

func handleAddTracker(ctx context.Context, pool *pgxpool.Pool, tg *telegram.Client, chatID int64, userID, url string, log zerolog.Logger, rend *renderer.Renderer, cookiesFile string) {
	fetcher := extractor.NewPageFetcher(rend, cookiesFile)
	body, err := fetcher.Fetch(url)
	if err != nil {
		SendTelegramMessage(tg, chatID, "Не удалось загрузить страницу: "+err.Error())
		return
	}

	zara := extractor.NewZara()
	result, err := zara.Extract(body, url)
	if err != nil || len(result.Candidates) == 0 {
		generic := extractor.NewGeneric()
		result, err = generic.Extract(body, url)
	}
	if err != nil || len(result.Candidates) == 0 {
		SendTelegramMessage(tg, chatID, "Не удалось извлечь цену со страницы. Укажите цену вручную.")
		return
	}

	candidate := result.Candidates[0]
	newPrice := 0.0
	fmt.Sscanf(candidate.Price, "%f", &newPrice)

	var trackerID string
	err = pool.QueryRow(ctx, `
		INSERT INTO trackers (user_id, url, normalized_url, domain, title, image_url, initial_price, current_price, currency, current_stock_status, extraction_rule, extraction_confidence, status)
		VALUES ($1, $2, $2, $3, $4, $5, $6, $6, $7, $8, $9, $10, 'active')
		RETURNING id
	`, userID, url, extractDomain(url), result.Title, result.ImageURL, newPrice, candidate.Currency, result.StockStatus, candidate.Rule, candidate.Confidence).Scan(&trackerID)
	if err != nil {
		log.Error().Err(err).Msg("failed to create tracker")
		SendTelegramMessage(tg, chatID, "Ошибка при создании трекера.")
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
	SendTelegramMessage(tg, chatID, fmt.Sprintf("✅ Трекер создан!\n\n%s\n%s\n📦 %s\nID: <code>%s</code>", title, formatMoney(newPrice), result.StockStatus, trackerID[:8]))
}

func handleDeleteTracker(ctx context.Context, pool *pgxpool.Pool, tg *telegram.Client, chatID int64, userID, trackerID string, log zerolog.Logger) {
	tag, err := pool.Exec(ctx, `DELETE FROM trackers WHERE id::text LIKE $1 || '%' AND user_id = $2`, trackerID, userID)
	if err != nil {
		SendTelegramMessage(tg, chatID, "Ошибка при удалении трекера.")
		return
	}
	if tag.RowsAffected() == 0 {
		SendTelegramMessage(tg, chatID, "Трекер не найден.")
		return
	}
	SendTelegramMessage(tg, chatID, "✅ Трекер удалён.")
}

func handleCheckTracker(ctx context.Context, pool *pgxpool.Pool, tg *telegram.Client, chatID int64, userID, trackerID string, log zerolog.Logger, rend *renderer.Renderer, cookiesFile string) {
	var url, currency string
	err := pool.QueryRow(ctx, `SELECT url, currency FROM trackers WHERE id::text LIKE $1 || '%' AND user_id = $2`, trackerID, userID).Scan(&url, &currency)
	if err != nil {
		SendTelegramMessage(tg, chatID, "Трекер не найден.")
		return
	}

	fetcher := extractor.NewPageFetcher(rend, cookiesFile)
	body, err := fetcher.Fetch(url)
	if err != nil {
		SendTelegramMessage(tg, chatID, "Не удалось загрузить страницу.")
		return
	}

	zara := extractor.NewZara()
	result, err := zara.Extract(body, url)
	if err != nil || len(result.Candidates) == 0 {
		generic := extractor.NewGeneric()
		result, err = generic.Extract(body, url)
	}
	if err != nil || len(result.Candidates) == 0 {
		SendTelegramMessage(tg, chatID, "Не удалось извлечь данные.")
		return
	}

	candidate := result.Candidates[0]
	newPrice := 0.0
	fmt.Sscanf(candidate.Price, "%f", &newPrice)

	pool.Exec(ctx, `INSERT INTO price_points (id, tracker_id, price, currency, source, status) VALUES (gen_random_uuid(), $1, $2, $3, 'manual_check', 'success')`, trackerID, newPrice, candidate.Currency)
	pool.Exec(ctx, `INSERT INTO stock_points (id, tracker_id, stock_status, source, status) VALUES (gen_random_uuid(), $1, $2, 'manual_check', 'success')`, trackerID, result.StockStatus)
	nextCurrency := candidate.Currency
	if nextCurrency == "" {
		nextCurrency = currency
	}
	pool.Exec(ctx, `UPDATE trackers SET current_price = $2, current_stock_status = $3, currency = $4, last_checked_at = now(), next_check_at = now() + (check_interval_minutes * interval '1 minute'), consecutive_errors = 0, last_error = NULL WHERE id::text LIKE $1 || '%'`, trackerID, newPrice, result.StockStatus, nextCurrency)

	SendTelegramMessage(tg, chatID, fmt.Sprintf("✅ Проверка завершена\n%s\n📦 %s", formatMoney(newPrice), result.StockStatus))
}

func handleTrackerHistory(ctx context.Context, pool *pgxpool.Pool, tg *telegram.Client, chatID int64, userID, trackerID string, log zerolog.Logger) {
	var title string
	err := pool.QueryRow(ctx, `SELECT title FROM trackers WHERE id::text LIKE $1 || '%' AND user_id = $2`, trackerID, userID).Scan(&title)
	if err != nil {
		SendTelegramMessage(tg, chatID, "Трекер не найден.")
		return
	}

	priceRows, err := pool.Query(ctx, `
		SELECT price, currency, checked_at FROM price_points
		WHERE tracker_id = $1 ORDER BY checked_at DESC LIMIT 10
	`, trackerID)
	if err != nil {
		SendTelegramMessage(tg, chatID, "Ошибка загрузки истории.")
		return
	}
	defer priceRows.Close()

	var lines []string
	lines = append(lines, fmt.Sprintf("📊 <b>%s</b>", truncate(title, 30)))
	lines = append(lines, "\n<i>История цен:</i>")

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
		lines = append(lines, "Нет данных")
	}

	SendTelegramMessage(tg, chatID, strings.Join(lines, "\n"))
}

func extractDomain(url string) string {
	url = strings.TrimPrefix(url, "https://")
	url = strings.TrimPrefix(url, "http://")
	parts := strings.Split(url, "/")
	if len(parts) > 0 {
		return parts[0]
	}
	return url
}

func isURL(text string) bool {
	return strings.HasPrefix(text, "https://") || strings.HasPrefix(text, "http://")
}

func parsePriceInput(text string) (float64, string, bool) {
	re := regexp.MustCompile(`\d+(?:[\s.,]\d+)*`)
	match := re.FindString(text)
	if match == "" {
		return 0, "PLN", false
	}
	normalized := normalizePriceNumber(match)
	price, err := strconv.ParseFloat(normalized, 64)
	return price, detectCurrency(text), err == nil && price > 0
}

func formatPriceForSearch(price float64) string {
	if price == float64(int64(price)) {
		return fmt.Sprintf("%.0f", price)
	}
	return fmt.Sprintf("%.2f", price)
}

func formatMoney(price float64) string {
	return fmt.Sprintf("💰 %.2f", price)
}

func parseIntervalMinutes(text string) (int, bool) {
	re := regexp.MustCompile(`\d+`)
	match := re.FindString(text)
	if match == "" {
		return 0, false
	}
	minutes, err := strconv.Atoi(match)
	if err != nil {
		return 0, false
	}
	lower := strings.ToLower(text)
	if strings.Contains(lower, "час") || strings.Contains(lower, "hour") {
		minutes *= 60
	}
	return minutes, minutes > 0
}

func formatInterval(minutes int) string {
	if minutes <= 0 {
		minutes = 180
	}
	if minutes%60 == 0 {
		hours := minutes / 60
		switch hours {
		case 1:
			return "каждый час"
		case 2, 3, 4:
			return fmt.Sprintf("каждые %d часа", hours)
		default:
			return fmt.Sprintf("каждые %d часов", hours)
		}
	}
	return fmt.Sprintf("каждые %d минут", minutes)
}

func normalizePriceNumber(text string) string {
	normalized := strings.ReplaceAll(text, " ", "")
	if strings.Contains(normalized, ",") && strings.Contains(normalized, ".") {
		if strings.LastIndex(normalized, ",") > strings.LastIndex(normalized, ".") {
			normalized = strings.ReplaceAll(normalized, ".", "")
			normalized = strings.ReplaceAll(normalized, ",", ".")
		} else {
			normalized = strings.ReplaceAll(normalized, ",", "")
		}
		return normalized
	}
	if strings.Count(normalized, ",") == 1 {
		return strings.ReplaceAll(normalized, ",", ".")
	}
	return strings.ReplaceAll(normalized, ",", "")
}

func detectCurrency(text string) string {
	lower := strings.ToLower(text)
	switch {
	case strings.Contains(lower, "zł"), strings.Contains(lower, "pln"):
		return "PLN"
	case strings.Contains(lower, "€"), strings.Contains(lower, "eur"):
		return "EUR"
	case strings.Contains(lower, "$"), strings.Contains(lower, "usd"):
		return "USD"
	case strings.Contains(lower, "£"), strings.Contains(lower, "gbp"):
		return "GBP"
	case strings.Contains(lower, "₽"), strings.Contains(lower, "rub"):
		return "RUB"
	default:
		return "PLN"
	}
}

func truncate(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n]) + "..."
}
