package notifier

import (
	"context"
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

func New(pool *pgxpool.Pool, tg *telegram.Client, log zerolog.Logger) *Notifier {
	return &Notifier{pool: pool, tg: tg, log: log}
}

func (n *Notifier) SendPending(ctx context.Context) {
	rows, err := n.pool.Query(ctx, `
		SELECT n.id, n.type, n.tracker_id, n.user_id,
		       n.old_price, n.new_price, n.currency,
		       n.old_stock_status, n.new_stock_status,
		       t.title, t.url, t.current_price, t.currency,
		       tl.telegram_id
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
			telegramChatID  int64
		)
		if err := rows.Scan(&id, &notifType, &trackerID, &userID,
			&oldPrice, &newPrice, &currency,
			&oldStockStatus, &newStockStatus,
			&title, &url, &currentPrice, &trackerCurrency,
			&telegramChatID); err != nil {
			n.log.Error().Err(err).Msg("failed to scan notification")
			continue
		}
		n.send(ctx, id, notifType, title, url, oldPrice, newPrice, currency, oldStockStatus, newStockStatus, currentPrice, trackerCurrency, telegramChatID)
	}
}

func (n *Notifier) send(ctx context.Context, id, notifType string, title *string, url string,
	oldPrice, newPrice *float64, currency *string,
	oldStockStatus, newStockStatus *string,
	currentPrice *float64, trackerCurrency string,
	chatID int64) {

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
		oldStr := "—"
		newStr := "—"
		if oldPrice != nil {
			oldStr = formatMoney(*oldPrice)
		}
		if newPrice != nil {
			newStr = formatMoney(*newPrice)
		}
		text = fmt.Sprintf("🔔 Цена изменилась\n\nТовар: %s\nСтарая цена: %s\nНовая цена: %s\n\nОткрыть товар:\n%s",
			displayTitle, oldStr, newStr, url)

	case "back_in_stock":
		text = fmt.Sprintf("✅ Товар снова в наличии\n\nТовар: %s\nЦена: %s\n\nОткрыть товар:\n%s",
			displayTitle, priceStr, url)

	case "out_of_stock":
		text = fmt.Sprintf("❌ Товар закончился\n\nТовар: %s\nПоследняя цена: %s\n\nОткрыть товар:\n%s",
			displayTitle, priceStr, url)

	case "stock_changed":
		oldStr := "неизвестно"
		newStr := "неизвестно"
		if oldStockStatus != nil {
			oldStr = stockLabel(*oldStockStatus)
		}
		if newStockStatus != nil {
			newStr = stockLabel(*newStockStatus)
		}
		text = fmt.Sprintf("📦 Статус наличия изменился\n\nТовар: %s\nБыло: %s\nСтало: %s\nЦена: %s\n\nОткрыть товар:\n%s",
			displayTitle, oldStr, newStr, priceStr, url)

	case "extraction_failed":
		text = fmt.Sprintf("⚠️ Ошибка проверки цены\n\nТовар: %s\nНе удалось извлечь цену. Возможно, сайт изменился.\n\nОткрыть товар:\n%s",
			displayTitle, url)

	default:
		n.log.Warn().Str("type", notifType).Msg("unknown notification type")
		return
	}

	if err := n.tg.SendMessage(chatID, text); err != nil {
		n.log.Error().Err(err).Str("notification_id", id).Msg("failed to send notification")
		n.pool.Exec(ctx, `UPDATE notifications SET status = 'failed', error_message = $2 WHERE id = $1`, id, err.Error())
		return
	}

	n.pool.Exec(ctx, `UPDATE notifications SET status = 'sent', sent_at = now() WHERE id = $1`, id)
	n.log.Info().Str("notification_id", id).Str("type", notifType).Int64("chat_id", chatID).Msg("notification sent")
}

func formatMoney(price float64) string {
	return fmt.Sprintf("💰 %.2f", price)
}

func stockLabel(s string) string {
	switch s {
	case "in_stock":
		return "в наличии"
	case "out_of_stock":
		return "нет в наличии"
	default:
		return s
	}
}
