package notifier

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"

	"github.com/littlewell/price-tracker/internal/telegram"
)

type Notifier struct {
	pool *pgxpool.Pool
	tg   *telegram.Client
	log  zerolog.Logger
}

var notifierTexts = map[string]map[string]string{
	"en": {
		"price_changed":       "🔔 Price changed\n\nProduct: %s\nInitial price: %s\nPrevious price: %s\nCurrent price: %s\n\nOpen product:\n%s",
		"price_changed_first": "🔔 Price changed\n\nProduct: %s\nInitial price: %s\nNew price: %s\n\nOpen product:\n%s",
		"back_in_stock":       "✅ Product is back in stock\n\nProduct: %s\nPrice: %s\n\nOpen product:\n%s",
		"out_of_stock":        "❌ Product is out of stock\n\nProduct: %s\nLast price: %s\n\nOpen product:\n%s",
		"stock_changed":       "📦 Stock status changed\n\nProduct: %s\nBefore: %s\nNow: %s\nPrice: %s\n\nOpen product:\n%s",
		"extraction_failed":   "⚠️ Price check failed\n\nProduct: %s\nCould not extract the price. The site may have changed.\n\nOpen product:\n%s",
		"manual_check_price":  "✅ Check finished\n\nProduct: %s\nPrice unchanged: %s\n\nOpen product:\n%s",
		"manual_check_stock":  "✅ Check finished\n\nProduct: %s\nStock status unchanged: %s\n\nOpen product:\n%s",
		"trial_expired":       "⏳ Your trial has ended\n\nYou're back on the Free plan, so this tracker was stopped and moved to your history:\n\nProduct: %s\n\nOpen product:\n%s",
		"unknown":             "unknown",
		"stock_in":            "in stock",
		"stock_out":           "out of stock",
		"stop_tracking":       "Stop tracking",
	},
	"ru": {
		"price_changed":       "🔔 Цена изменилась\n\nТовар: %s\nИзначальная цена: %s\nПрошлая цена: %s\nТекущая цена: %s\n\nОткрыть товар:\n%s",
		"price_changed_first": "🔔 Цена изменилась\n\nТовар: %s\nИзначальная цена: %s\nНовая цена: %s\n\nОткрыть товар:\n%s",
		"back_in_stock":       "✅ Товар снова в наличии\n\nТовар: %s\nЦена: %s\n\nОткрыть товар:\n%s",
		"out_of_stock":        "❌ Товар закончился\n\nТовар: %s\nПоследняя цена: %s\n\nОткрыть товар:\n%s",
		"stock_changed":       "📦 Статус наличия изменился\n\nТовар: %s\nБыло: %s\nСтало: %s\nЦена: %s\n\nОткрыть товар:\n%s",
		"extraction_failed":   "⚠️ Ошибка проверки цены\n\nТовар: %s\nНе удалось извлечь цену. Возможно, сайт изменился.\n\nОткрыть товар:\n%s",
		"manual_check_price":  "✅ Проверка завершена\n\nТовар: %s\nЦена не изменилась: %s\n\nОткрыть товар:\n%s",
		"manual_check_stock":  "✅ Проверка завершена\n\nТовар: %s\nНаличие не изменилось: %s\n\nОткрыть товар:\n%s",
		"trial_expired":       "⏳ Пробный период закончился\n\nВы вернулись на тариф Free, поэтому этот трекер остановлен и перенесён в историю:\n\nТовар: %s\n\nОткрыть товар:\n%s",
		"unknown":             "неизвестно",
		"stock_in":            "в наличии",
		"stock_out":           "нет в наличии",
		"stop_tracking":       "Остановить отслеживание",
	},
	"pl": {
		"price_changed":       "🔔 Cena się zmieniła\n\nProdukt: %s\nCena początkowa: %s\nPoprzednia cena: %s\nAktualna cena: %s\n\nOtwórz produkt:\n%s",
		"price_changed_first": "🔔 Cena się zmieniła\n\nProdukt: %s\nCena początkowa: %s\nNowa cena: %s\n\nOtwórz produkt:\n%s",
		"back_in_stock":       "✅ Produkt znów jest dostępny\n\nProdukt: %s\nCena: %s\n\nOtwórz produkt:\n%s",
		"out_of_stock":        "❌ Produkt jest niedostępny\n\nProdukt: %s\nOstatnia cena: %s\n\nOtwórz produkt:\n%s",
		"stock_changed":       "📦 Status dostępności się zmienił\n\nProdukt: %s\nByło: %s\nTeraz: %s\nCena: %s\n\nOtwórz produkt:\n%s",
		"extraction_failed":   "⚠️ Błąd sprawdzania ceny\n\nProdukt: %s\nNie udało się pobrać ceny. Strona mogła się zmienić.\n\nOtwórz produkt:\n%s",
		"manual_check_price":  "✅ Sprawdzanie zakończone\n\nProdukt: %s\nCena bez zmian: %s\n\nOtwórz produkt:\n%s",
		"manual_check_stock":  "✅ Sprawdzanie zakończone\n\nProdukt: %s\nDostępność bez zmian: %s\n\nOtwórz produkt:\n%s",
		"trial_expired":       "⏳ Okres próbny się zakończył\n\nWracasz na plan Free, więc ten tracker został zatrzymany i przeniesiony do historii:\n\nProdukt: %s\n\nOtwórz produkt:\n%s",
		"unknown":             "nieznany",
		"stock_in":            "dostępny",
		"stock_out":           "niedostępny",
		"stop_tracking":       "Zatrzymaj śledzenie",
	},
}

func nt(lang, key string) string {
	if texts, ok := notifierTexts[lang]; ok {
		if text, ok := texts[key]; ok {
			return text
		}
	}
	return notifierTexts["en"][key]
}

func notifierLanguage(lang string) string {
	switch lang {
	case "ru", "en", "pl":
		return lang
	default:
		return "en"
	}
}

func New(pool *pgxpool.Pool, tg *telegram.Client, log zerolog.Logger) *Notifier {
	return &Notifier{pool: pool, tg: tg, log: log}
}

func (n *Notifier) SendPending(ctx context.Context) {
	rows, err := n.pool.Query(ctx, `
		SELECT n.id, n.type, n.tracker_id, n.user_id,
		       n.old_price, n.new_price, n.currency,
		       n.old_stock_status, n.new_stock_status,
		       t.title, t.url, t.current_price, t.currency, t.initial_price,
		       tl.telegram_id, COALESCE(tl.language, 'en')
		FROM notifications n
		JOIN trackers t ON t.id = n.tracker_id
		JOIN telegram_links tl ON tl.user_id = n.user_id
		WHERE n.status = 'pending'
		ORDER BY n.created_at
		LIMIT 50
		FOR UPDATE SKIP LOCKED
	`)
	if err != nil {
		n.log.Error().Err(err).Msg("failed to query pending notifications")
		return
	}
	defer rows.Close()

	for rows.Next() {
		var (
			id              string
			notifType       string
			trackerID       string
			userID          string
			oldPrice        *float64
			newPrice        *float64
			currency        *string
			oldStockStatus  *string
			newStockStatus  *string
			title           *string
			url             string
			currentPrice    *float64
			trackerCurrency string
			initialPrice    float64
			telegramChatID  int64
			lang            string
		)
		if err := rows.Scan(&id, &notifType, &trackerID, &userID,
			&oldPrice, &newPrice, &currency,
			&oldStockStatus, &newStockStatus,
			&title, &url, &currentPrice, &trackerCurrency, &initialPrice,
			&telegramChatID, &lang); err != nil {
			n.log.Error().Err(err).Msg("failed to scan notification")
			continue
		}
		n.send(ctx, id, notifType, trackerID, title, url, oldPrice, newPrice, currency, oldStockStatus, newStockStatus, currentPrice, trackerCurrency, initialPrice, telegramChatID, notifierLanguage(lang))
	}
}

func (n *Notifier) send(ctx context.Context, id, notifType, trackerID string, title *string, url string,
	oldPrice, newPrice *float64, currency *string,
	oldStockStatus, newStockStatus *string,
	currentPrice *float64, trackerCurrency string, initialPrice float64,
	chatID int64, lang string) {

	displayTitle := url
	if title != nil && *title != "" {
		displayTitle = *title
	}

	priceStr := ""
	if currentPrice != nil {
		priceStr = formatMoney(*currentPrice)
	}

	var text string

	switch notifType {
	case "price_changed":
		newStr := "—"
		if newPrice != nil {
			newStr = formatMoney(*newPrice)
		}
		initialStr := formatMoney(initialPrice)
		// oldPrice is nil specifically for a tracker's first-ever price change (see
		// cmd/worker/main.go's isFirstChange), since there's no distinct "previous
		// price" yet to show beyond the initial one.
		if oldPrice == nil {
			text = fmt.Sprintf(nt(lang, "price_changed_first"), displayTitle, initialStr, newStr, url)
		} else {
			text = fmt.Sprintf(nt(lang, "price_changed"), displayTitle, initialStr, formatMoney(*oldPrice), newStr, url)
		}

	case "back_in_stock":
		text = fmt.Sprintf(nt(lang, "back_in_stock"), displayTitle, priceStr, url)

	case "out_of_stock":
		text = fmt.Sprintf(nt(lang, "out_of_stock"), displayTitle, priceStr, url)

	case "stock_changed":
		oldStr := nt(lang, "unknown")
		newStr := nt(lang, "unknown")
		if oldStockStatus != nil {
			oldStr = stockLabel(lang, *oldStockStatus)
		}
		if newStockStatus != nil {
			newStr = stockLabel(lang, *newStockStatus)
		}
		text = fmt.Sprintf(nt(lang, "stock_changed"), displayTitle, oldStr, newStr, priceStr, url)

	case "extraction_failed":
		text = fmt.Sprintf(nt(lang, "extraction_failed"), displayTitle, url)

	case "manual_check_ok":
		// The worker sets new_price for price trackers and new_stock_status for
		// stock trackers; whichever is present decides the wording.
		if newPrice != nil {
			text = fmt.Sprintf(nt(lang, "manual_check_price"), displayTitle, formatMoney(*newPrice), url)
		} else {
			statusStr := nt(lang, "unknown")
			if newStockStatus != nil {
				statusStr = stockLabel(lang, *newStockStatus)
			}
			text = fmt.Sprintf(nt(lang, "manual_check_stock"), displayTitle, statusStr, url)
		}

	case "trial_expired":
		// No "stop tracking" button below for this type: the tracker is already stopped,
		// which is precisely what this message is telling the user.
		text = fmt.Sprintf(nt(lang, "trial_expired"), displayTitle, url)

	default:
		n.log.Warn().Str("type", notifType).Msg("unknown notification type")
		return
	}

	var sendErr error
	if notifType == "price_changed" || notifType == "back_in_stock" {
		markup, _ := json.Marshal(map[string]interface{}{
			"inline_keyboard": [][]map[string]string{{{
				"text": nt(lang, "stop_tracking"), "callback_data": "tracker:complete:" + trackerID,
			}}},
		})
		sendErr = n.tg.SendMessageWithMarkup(chatID, text, markup)
	} else {
		sendErr = n.tg.SendMessage(chatID, text)
	}
	if sendErr != nil {
		n.log.Error().Err(sendErr).Str("notification_id", id).Msg("failed to send notification")
		n.pool.Exec(ctx, `UPDATE notifications SET status = 'failed', error_message = $2 WHERE id = $1`, id, sendErr.Error())
		return
	}

	n.pool.Exec(ctx, `UPDATE notifications SET status = 'sent', sent_at = now() WHERE id = $1`, id)
	n.log.Info().Str("notification_id", id).Str("type", notifType).Int64("chat_id", chatID).Msg("notification sent")
}

func formatMoney(price float64) string {
	return fmt.Sprintf("💰 %.2f", price)
}

func stockLabel(lang, s string) string {
	switch s {
	case "in_stock":
		return nt(lang, "stock_in")
	case "out_of_stock":
		return nt(lang, "stock_out")
	default:
		return s
	}
}
