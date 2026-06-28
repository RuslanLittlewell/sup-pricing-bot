package handler

import (
	"crypto/rand"
	"fmt"
	"math/big"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/littlewell/price-tracker/internal/telegram"
)

func GetUserFromRequest(r *http.Request, pool *pgxpool.Pool) (string, error) {
	token := r.Header.Get("X-Session-Token")
	if token == "" {
		return "", fmt.Errorf("no session token")
	}

	var userID string
	err := pool.QueryRow(r.Context(), `
		SELECT user_id FROM "session"
		WHERE token = $1 AND expires_at > now()
	`, token).Scan(&userID)
	if err != nil {
		return "", fmt.Errorf("invalid session")
	}
	return userID, nil
}

func SendTelegramMessage(tg *telegram.Client, chatID int64, text string) {
	if err := tg.SendMessage(chatID, text); err != nil {
	}
}

func GenerateLinkCode() string {
	const charset = "ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	b := make([]byte, 6)
	for i := range b {
		n, _ := rand.Int(rand.Reader, big.NewInt(int64(len(charset))))
		b[i] = charset[n.Int64()]
	}
	return string(b)
}
