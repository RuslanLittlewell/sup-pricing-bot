package handler

import (
	"github.com/littlewell/price-tracker/internal/telegram"
)

func SendTelegramMessage(tg *telegram.Client, chatID int64, text string) {
	if err := tg.SendMessage(chatID, text); err != nil {
	}
}
