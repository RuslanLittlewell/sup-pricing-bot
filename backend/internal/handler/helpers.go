package handler

import (
	"github.com/littlewell/price-tracker/internal/telegram"
)

func SendTelegramMessage(tg *telegram.Client, chatID int64, text string) {
	_ = tg.SendMessage(chatID, text)
}
