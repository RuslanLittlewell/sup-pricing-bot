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

const defaultBotLanguage = "en"

var botTexts = map[string]map[string]string{
	"en": {
		"choose_language":         "Choose your language",
		"language_saved":          "Language saved. Send a product link to start tracking.",
		"account_not_linked":      "Account is not linked. Press /start.",
		"account_not_linked_site": "Account is not linked. Link Telegram on the website first: Profile → Link Telegram.",
		"link_invalid":            "The code is invalid or expired. Request a new one on the website.",
		"link_failed":             "Linking failed. Please try again.",
		"link_success":            "✅ Telegram linked. Choose your language.",
		"account_prepare_failed":  "Could not prepare your account. Please try again later.",
		"menu_start":              "Send a product link to track",
		"menu_back":               "Okay, back to menu.",
		"menu_stale":              "This choice is no longer active.",
		"menu_unknown_button":     "I did not understand this button.",
		"menu_unknown_interval":   "I did not understand the selected interval.",
		"help":                    "Send a product link, then the current price. Commands:\n/list — tracker list\n/delete <id> — delete\n/check <id> — check now\n/history <id> — change history",
		"send_link_or_help":       "Send a product link to track or /help.",
		"button_trackers":         "My trackers",
		"button_back":             "Back",
		"button_yes":              "Yes",
		"button_no":               "No",
		"button_edit":             "Edit",
		"button_delete":           "Delete",
		"button_choose_language":  "Language",
		"enter_price":             "Enter the current price",
		"save_link_failed":        "Could not save the link. Please try again.",
		"price_invalid":           "Enter a numeric price, for example: 1299, 1299.99 or 1299 zł",
		"confirm_prompt":          "Reply “yes” if this is the correct price block, or “no” to try the next one.",
		"search_unavailable":      "I cannot continue the search right now. Send the link again.",
		"search_started":          "Looking for the price block on the page...",
		"access_denied":           "The site blocked bot access. I cannot capture a price screenshot for this link yet.",
		"price_not_found":         "I did not find the price on the page. Check the link and price, then send the link again.",
		"no_more_candidates":      "I did not find other blocks with this price. Send the link again or try another price.",
		"save_candidate_failed":   "Could not save the found block. Please try again.",
		"candidate_caption":       "I found this block with price %s.\nIs this the correct price?",
		"tracker_create_failed":   "Could not create the tracker.",
		"tracker_created":         "✅ Tracker created!\n\n%s\n%s\nCheck every 3 hours.\nID: <code>%s</code>",
		"tracker_created_auto":    "✅ Tracker created!\n\n%s\n%s\n📦 %s\nID: <code>%s</code>",
		"tracker_delete_failed":   "Could not delete the tracker.",
		"trackers_load_failed":    "Could not load trackers.",
		"trackers_empty":          "You have no trackers. Send a product link to add the first one.",
		"interval_prompt":         "Choose scan frequency",
		"interval_invalid":        "Choose a frequency or enter minutes as a number.",
		"interval_save_failed":    "Could not save scan frequency.",
		"interval_saved":          "✅ Scan frequency updated: %s",
		"tracker_not_found":       "Tracker not found.",
		"tracker_deleted":         "✅ Tracker deleted.",
		"page_load_failed":        "Could not load the page.",
		"page_load_failed_detail": "Could not load the page: %s",
		"extract_failed":          "Could not extract data.",
		"extract_price_failed":    "Could not extract the price from the page. Enter the price manually.",
		"manual_check_done":       "✅ Check complete\n%s\n📦 %s",
		"history_load_failed":     "Could not load history.",
		"history_title":           "📊 <b>%s</b>\n\n<i>Price history:</i>",
		"history_empty":           "No data",
		"interval_every_hour":     "every hour",
		"interval_every_hours":    "every %d hours",
		"interval_every_minutes":  "every %d minutes",
	},
	"ru": {
		"choose_language":         "Выберите язык",
		"language_saved":          "Язык сохранён. Отправьте ссылку товара для трекинга.",
		"account_not_linked":      "Аккаунт не привязан. Нажмите /start.",
		"account_not_linked_site": "Аккаунт не привязан. Сначала привяжите Telegram на сайте: Профиль → Привязать Telegram.",
		"link_invalid":            "Код недействителен или истёк. Запросите новый на сайте.",
		"link_failed":             "Ошибка при привязке. Попробуйте снова.",
		"link_success":            "✅ Telegram привязан. Выберите язык.",
		"account_prepare_failed":  "Не удалось подготовить аккаунт. Попробуйте позже.",
		"menu_start":              "Введите ссылку товара для трекинга",
		"menu_back":               "Окей, вернулись назад.",
		"menu_stale":              "Этот выбор уже не актуален.",
		"menu_unknown_button":     "Не понял кнопку.",
		"menu_unknown_interval":   "Не понял выбранный интервал.",
		"help":                    "Отправьте ссылку товара, затем текущую цену. Команды:\n/list — список трекеров\n/delete <id> — удалить\n/check <id> — проверить сейчас\n/history <id> — история изменений",
		"send_link_or_help":       "Отправьте ссылку товара для трекинга или /help.",
		"button_trackers":         "Посмотреть мои трекеры",
		"button_back":             "Назад",
		"button_yes":              "Да",
		"button_no":               "Нет",
		"button_edit":             "Редактировать",
		"button_delete":           "Удалить",
		"button_choose_language":  "Язык",
		"enter_price":             "Введите текущую цену",
		"save_link_failed":        "Не удалось сохранить ссылку. Попробуйте ещё раз.",
		"price_invalid":           "Введите цену числом, например: 1299, 1299.99 или 1299 zł",
		"confirm_prompt":          "Ответьте «да», если блок с ценой найден верно, или «нет», чтобы попробовать следующий.",
		"search_unavailable":      "Не могу продолжить поиск прямо сейчас. Отправьте ссылку заново.",
		"search_started":          "Ищу блок с ценой на странице...",
		"access_denied":           "Сайт заблокировал загрузку страницы для бота. Для этой ссылки пока не могу снять скриншот цены.",
		"price_not_found":         "Не нашёл цену на странице. Проверьте ссылку и цену, затем отправьте ссылку заново.",
		"no_more_candidates":      "Других блоков с этой ценой не нашёл. Отправьте ссылку заново или другую цену.",
		"save_candidate_failed":   "Не удалось сохранить найденный блок. Попробуйте заново.",
		"candidate_caption":       "Нашёл этот блок с ценой %s.\nЭто правильная цена?",
		"tracker_create_failed":   "Ошибка при создании трекера.",
		"tracker_created":         "✅ Трекер создан!\n\n%s\n%s\nПроверка каждые 3 часа.\nID: <code>%s</code>",
		"tracker_created_auto":    "✅ Трекер создан!\n\n%s\n%s\n📦 %s\nID: <code>%s</code>",
		"tracker_delete_failed":   "Ошибка при удалении трекера.",
		"trackers_load_failed":    "Ошибка загрузки трекеров.",
		"trackers_empty":          "У вас нет трекеров. Отправьте ссылку товара, чтобы добавить первый.",
		"interval_prompt":         "Выберите интенсивность сканирования",
		"interval_invalid":        "Выберите интенсивность или введите количество минут числом.",
		"interval_save_failed":    "Ошибка при сохранении интенсивности.",
		"interval_saved":          "✅ Интенсивность обновлена: %s",
		"tracker_not_found":       "Трекер не найден.",
		"tracker_deleted":         "✅ Трекер удалён.",
		"page_load_failed":        "Не удалось загрузить страницу.",
		"page_load_failed_detail": "Не удалось загрузить страницу: %s",
		"extract_failed":          "Не удалось извлечь данные.",
		"extract_price_failed":    "Не удалось извлечь цену со страницы. Укажите цену вручную.",
		"manual_check_done":       "✅ Проверка завершена\n%s\n📦 %s",
		"history_load_failed":     "Ошибка загрузки истории.",
		"history_title":           "📊 <b>%s</b>\n\n<i>История цен:</i>",
		"history_empty":           "Нет данных",
		"interval_every_hour":     "каждый час",
		"interval_every_hours":    "каждые %d ч",
		"interval_every_minutes":  "каждые %d мин",
	},
	"pl": {
		"choose_language":         "Wybierz język",
		"language_saved":          "Język zapisany. Wyślij link do produktu, aby rozpocząć śledzenie.",
		"account_not_linked":      "Konto nie jest połączone. Naciśnij /start.",
		"account_not_linked_site": "Konto nie jest połączone. Najpierw połącz Telegram na stronie: Profil → Połącz Telegram.",
		"link_invalid":            "Kod jest nieprawidłowy albo wygasł. Poproś o nowy na stronie.",
		"link_failed":             "Nie udało się połączyć. Spróbuj ponownie.",
		"link_success":            "✅ Telegram połączony. Wybierz język.",
		"account_prepare_failed":  "Nie udało się przygotować konta. Spróbuj później.",
		"menu_start":              "Wyślij link do produktu do śledzenia",
		"menu_back":               "Dobrze, wracamy do menu.",
		"menu_stale":              "Ten wybór nie jest już aktualny.",
		"menu_unknown_button":     "Nie rozumiem tego przycisku.",
		"menu_unknown_interval":   "Nie rozumiem wybranego interwału.",
		"help":                    "Wyślij link do produktu, potem aktualną cenę. Komendy:\n/list — lista trackerów\n/delete <id> — usuń\n/check <id> — sprawdź teraz\n/history <id> — historia zmian",
		"send_link_or_help":       "Wyślij link do produktu albo /help.",
		"button_trackers":         "Moje trackery",
		"button_back":             "Wstecz",
		"button_yes":              "Tak",
		"button_no":               "Nie",
		"button_edit":             "Edytuj",
		"button_delete":           "Usuń",
		"button_choose_language":  "Język",
		"enter_price":             "Wpisz aktualną cenę",
		"save_link_failed":        "Nie udało się zapisać linku. Spróbuj ponownie.",
		"price_invalid":           "Wpisz cenę jako liczbę, np. 1299, 1299.99 albo 1299 zł",
		"confirm_prompt":          "Odpowiedz “tak”, jeśli blok ceny jest poprawny, albo “nie”, aby sprawdzić następny.",
		"search_unavailable":      "Nie mogę teraz kontynuować wyszukiwania. Wyślij link ponownie.",
		"search_started":          "Szukam bloku z ceną na stronie...",
		"access_denied":           "Strona zablokowała dostęp bota. Nie mogę jeszcze zrobić zrzutu ceny dla tego linku.",
		"price_not_found":         "Nie znalazłem ceny na stronie. Sprawdź link i cenę, potem wyślij link ponownie.",
		"no_more_candidates":      "Nie znalazłem innych bloków z tą ceną. Wyślij link ponownie albo inną cenę.",
		"save_candidate_failed":   "Nie udało się zapisać znalezionego bloku. Spróbuj ponownie.",
		"candidate_caption":       "Znalazłem ten blok z ceną %s.\nCzy to poprawna cena?",
		"tracker_create_failed":   "Nie udało się utworzyć trackera.",
		"tracker_created":         "✅ Tracker utworzony!\n\n%s\n%s\nSprawdzanie co 3 godziny.\nID: <code>%s</code>",
		"tracker_created_auto":    "✅ Tracker utworzony!\n\n%s\n%s\n📦 %s\nID: <code>%s</code>",
		"tracker_delete_failed":   "Nie udało się usunąć trackera.",
		"trackers_load_failed":    "Nie udało się załadować trackerów.",
		"trackers_empty":          "Nie masz trackerów. Wyślij link do produktu, aby dodać pierwszy.",
		"interval_prompt":         "Wybierz częstotliwość skanowania",
		"interval_invalid":        "Wybierz częstotliwość albo wpisz liczbę minut.",
		"interval_save_failed":    "Nie udało się zapisać częstotliwości.",
		"interval_saved":          "✅ Częstotliwość zaktualizowana: %s",
		"tracker_not_found":       "Nie znaleziono trackera.",
		"tracker_deleted":         "✅ Tracker usunięty.",
		"page_load_failed":        "Nie udało się załadować strony.",
		"page_load_failed_detail": "Nie udało się załadować strony: %s",
		"extract_failed":          "Nie udało się pobrać danych.",
		"extract_price_failed":    "Nie udało się pobrać ceny ze strony. Wpisz cenę ręcznie.",
		"manual_check_done":       "✅ Sprawdzanie zakończone\n%s\n📦 %s",
		"history_load_failed":     "Nie udało się załadować historii.",
		"history_title":           "📊 <b>%s</b>\n\n<i>Historia cen:</i>",
		"history_empty":           "Brak danych",
		"interval_every_hour":     "co godzinę",
		"interval_every_hours":    "co %d godz.",
		"interval_every_minutes":  "co %d min",
	},
}

func tr(lang, key string) string {
	if texts, ok := botTexts[lang]; ok {
		if text, ok := texts[key]; ok {
			return text
		}
	}
	return botTexts[defaultBotLanguage][key]
}

func validBotLanguage(lang string) string {
	switch lang {
	case "ru", "en", "pl":
		return lang
	default:
		return defaultBotLanguage
	}
}

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
			handleTelegramCallback(ctx, pool, tg, chatID, userID, lang, callback.Data, log, rend)
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
		case handleTrackerDialog(ctx, pool, tg, from.ID, userID, lang, text, log, rend):
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

func sendLanguageMenu(tg *telegram.Client, chatID int64) {
	markup := makeInlineKeyboard(
		[]inlineButton{button("English", "lang:en")},
		[]inlineButton{button("Русский", "lang:ru"), button("Polski", "lang:pl")},
	)
	_ = tg.SendMessageWithMarkup(chatID, tr(defaultBotLanguage, "choose_language"), markup)
}

func sendMainMenu(tg *telegram.Client, chatID int64, lang, text string) {
	markup := makeInlineKeyboard(
		[]inlineButton{button(tr(lang, "button_trackers"), "menu:list")},
		[]inlineButton{button(tr(lang, "button_choose_language"), "menu:language")},
	)
	_ = tg.SendMessageWithMarkup(chatID, text, markup)
}

func sendBackMessage(tg *telegram.Client, chatID int64, lang, text string) {
	markup := makeInlineKeyboard([]inlineButton{button(tr(lang, "button_back"), "menu:back")})
	_ = tg.SendMessageWithMarkup(chatID, text, markup)
}

func handleTelegramCallback(ctx context.Context, pool *pgxpool.Pool, tg *telegram.Client, chatID int64, userID, lang, data string, log zerolog.Logger, rend *renderer.Renderer) {
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
		sendNextPriceCandidate(ctx, pool, tg, chatID, userID, lang, state.URL, state.InitialPrice, state.Currency, state.CandidateIndex+1, log, rend)
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
		sendIntervalMenu(tg, chatID, lang, trackerID, tr(lang, "interval_prompt"))
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

func sendIntervalMenu(tg *telegram.Client, chatID int64, lang, trackerID, text string) {
	markup := makeInlineKeyboard(
		[]inlineButton{button(intervalButtonLabel(lang, 1), "interval:"+trackerID+":60"), button(intervalButtonLabel(lang, 3), "interval:"+trackerID+":180")},
		[]inlineButton{button(intervalButtonLabel(lang, 5), "interval:"+trackerID+":300"), button(intervalButtonLabel(lang, 24), "interval:"+trackerID+":1440")},
		[]inlineButton{button(tr(lang, "button_back"), "menu:list")},
	)
	_ = tg.SendMessageWithMarkup(chatID, text, markup)
}

func intervalButtonLabel(lang string, hours int) string {
	if lang == "ru" {
		return fmt.Sprintf("%dч", hours)
	}
	return fmt.Sprintf("%dh", hours)
}

func updateTrackerInterval(ctx context.Context, pool *pgxpool.Pool, tg *telegram.Client, chatID int64, userID, lang, trackerID string, minutes int, log zerolog.Logger) {
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

func handleTrackerDialog(ctx context.Context, pool *pgxpool.Pool, tg *telegram.Client, chatID int64, userID, lang, text string, log zerolog.Logger, rend *renderer.Renderer) bool {
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
		_, err := pool.Exec(ctx, `
			INSERT INTO telegram_states (telegram_id, user_id, step, url)
			VALUES ($1, $2, 'awaiting_price', $3)
			ON CONFLICT (telegram_id) DO UPDATE
			SET user_id = $2, step = 'awaiting_price', url = $3, initial_price = NULL,
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
		sendBackMessage(tg, chatID, lang, tr(lang, "enter_price"))
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
			sendBackMessage(tg, chatID, lang, tr(lang, "price_invalid"))
			return true
		}
		log.Info().
			Int64("telegram_id", chatID).
			Str("url", state.URL).
			Float64("price", price).
			Str("currency", currency).
			Msg("telegram price input accepted")
		sendNextPriceCandidate(ctx, pool, tg, chatID, userID, lang, state.URL, price, currency, 0, log, rend)
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
			sendNextPriceCandidate(ctx, pool, tg, chatID, userID, lang, state.URL, state.InitialPrice, state.Currency, state.CandidateIndex+1, log, rend)
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
			sendIntervalMenu(tg, chatID, lang, state.URL, tr(lang, "interval_invalid"))
			return true
		}
		updateTrackerInterval(ctx, pool, tg, chatID, userID, lang, state.URL, interval, log)
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

func sendNextPriceCandidate(ctx context.Context, pool *pgxpool.Pool, tg *telegram.Client, chatID int64, userID, lang, url string, price float64, currency string, index int, log zerolog.Logger, rend *renderer.Renderer) {
	if rend == nil {
		SendTelegramMessage(tg, chatID, tr(lang, "search_unavailable"))
		clearTelegramState(ctx, pool, chatID)
		return
	}

	SendTelegramMessage(tg, chatID, tr(lang, "search_started"))
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
		INSERT INTO telegram_states (telegram_id, user_id, step, url, initial_price, currency, candidate_index, rule)
		VALUES ($1, $2, 'awaiting_confirm', $3, $4, $5, $6, $7)
		ON CONFLICT (telegram_id) DO UPDATE
		SET user_id = $2, step = 'awaiting_confirm', url = $3, initial_price = $4,
		    currency = $5, candidate_index = $6, rule = $7, updated_at = now()
	`, chatID, userID, url, price, currency, index, rule)
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

func createTrackerFromState(ctx context.Context, pool *pgxpool.Pool, tg *telegram.Client, chatID int64, userID, lang string, state telegramState, log zerolog.Logger) {
	var trackerID string
	err := pool.QueryRow(ctx, `
		INSERT INTO trackers (user_id, url, normalized_url, domain, title, initial_price, current_price, currency, current_stock_status, extraction_rule, extraction_confidence, status, next_check_at)
		VALUES ($1, $2, $2, $3, $3, $4, $4, $5, 'unknown', $6, 0.9, 'active', now() + interval '3 hours')
		RETURNING id
	`, userID, state.URL, extractDomain(state.URL), state.InitialPrice, state.Currency, state.Rule).Scan(&trackerID)
	if err != nil {
		log.Error().Err(err).Msg("failed to create tracker from telegram state")
		SendTelegramMessage(tg, chatID, tr(lang, "tracker_create_failed"))
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
		[]inlineButton{button(tr(lang, "button_trackers"), "menu:list")},
		[]inlineButton{button(tr(lang, "button_back"), "menu:back")},
	)
	_ = tg.SendMessageWithMarkup(chatID, fmt.Sprintf(tr(lang, "tracker_created"), state.URL, formatMoney(state.InitialPrice), trackerID[:8]), markup)
}

func clearTelegramState(ctx context.Context, pool *pgxpool.Pool, telegramID int64) {
	_, _ = pool.Exec(ctx, `DELETE FROM telegram_states WHERE telegram_id = $1`, telegramID)
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
		line += fmt.Sprintf("\n   ⏱ %s", formatInterval(lang, interval))
		line += fmt.Sprintf("\n   ID: <code>%s</code>", id[:8])
		lines = append(lines, line)
		shortID := id[:8]
		rowsMarkup = append(rowsMarkup,
			[]inlineButton{button(tr(lang, "button_edit"), "tracker:edit:"+shortID), button(tr(lang, "button_delete"), "tracker:delete:"+shortID)},
		)
	}

	if len(lines) == 0 {
		sendMainMenu(tg, chatID, lang, tr(lang, "trackers_empty"))
		return
	}

	msg := strings.Join(lines, "\n\n")
	rowsMarkup = append(rowsMarkup, []inlineButton{button(tr(lang, "button_back"), "menu:back")})
	_ = tg.SendMessageWithMarkup(chatID, msg, makeInlineKeyboard(rowsMarkup...))
}

func handleAddTracker(ctx context.Context, pool *pgxpool.Pool, tg *telegram.Client, chatID int64, userID, lang, url string, log zerolog.Logger, rend *renderer.Renderer, cookiesFile, proxyURL string) {
	fetcher := extractor.NewPageFetcher(rend, cookiesFile, proxyURL)
	body, err := fetcher.Fetch(url)
	if err != nil {
		SendTelegramMessage(tg, chatID, fmt.Sprintf(tr(lang, "page_load_failed_detail"), err.Error()))
		return
	}

	zara := extractor.NewZara()
	result, err := zara.Extract(body, url)
	if err != nil || len(result.Candidates) == 0 {
		generic := extractor.NewGeneric()
		result, err = generic.Extract(body, url)
	}
	if err != nil || len(result.Candidates) == 0 {
		SendTelegramMessage(tg, chatID, tr(lang, "extract_price_failed"))
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
	var url, currency string
	err := pool.QueryRow(ctx, `SELECT url, currency FROM trackers WHERE id::text LIKE $1 || '%' AND user_id = $2`, trackerID, userID).Scan(&url, &currency)
	if err != nil {
		SendTelegramMessage(tg, chatID, tr(lang, "tracker_not_found"))
		return
	}

	fetcher := extractor.NewPageFetcher(rend, cookiesFile, proxyURL)
	body, err := fetcher.Fetch(url)
	if err != nil {
		SendTelegramMessage(tg, chatID, tr(lang, "page_load_failed"))
		return
	}

	zara := extractor.NewZara()
	result, err := zara.Extract(body, url)
	if err != nil || len(result.Candidates) == 0 {
		generic := extractor.NewGeneric()
		result, err = generic.Extract(body, url)
	}
	if err != nil || len(result.Candidates) == 0 {
		SendTelegramMessage(tg, chatID, tr(lang, "extract_failed"))
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
	if strings.Contains(lower, "час") || strings.Contains(lower, "hour") || strings.Contains(lower, "godz") {
		minutes *= 60
	}
	return minutes, minutes > 0
}

func formatInterval(lang string, minutes int) string {
	if minutes <= 0 {
		minutes = 180
	}
	if minutes%60 == 0 {
		hours := minutes / 60
		if hours == 1 {
			return tr(lang, "interval_every_hour")
		}
		return fmt.Sprintf(tr(lang, "interval_every_hours"), hours)
	}
	return fmt.Sprintf(tr(lang, "interval_every_minutes"), minutes)
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
