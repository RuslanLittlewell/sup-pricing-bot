package handler

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func Me(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, err := GetUserFromRequest(r, pool)
		if err != nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		var user struct {
			ID       string `json:"id"`
			Email    string `json:"email"`
			Name     string `json:"name"`
			PlanCode string `json:"plan_code"`
		}
		err = pool.QueryRow(r.Context(), `
			SELECT u.id, u.email, u.name, COALESCE(up.plan_code, 'free')
			FROM "user" u
			LEFT JOIN user_plans up ON up.user_id = u.id
			WHERE u.id = $1
		`, userID).Scan(&user.ID, &user.Email, &user.Name, &user.PlanCode)
		if err != nil {
			http.Error(w, "user not found", http.StatusNotFound)
			return
		}
		json.NewEncoder(w).Encode(user)
	}
}

func TelegramLinkCode(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, err := GetUserFromRequest(r, pool)
		if err != nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}

		// Delete old codes
		pool.Exec(r.Context(), `DELETE FROM telegram_link_codes WHERE user_id = $1`, userID)

		code := GenerateLinkCode()
		_, err = pool.Exec(r.Context(), `
			INSERT INTO telegram_link_codes (code, user_id, expires_at)
			VALUES ($1, $2, now() + interval '5 minutes')
		`, code, userID)
		if err != nil {
			http.Error(w, "failed to generate code", http.StatusInternalServerError)
			return
		}

		json.NewEncoder(w).Encode(map[string]interface{}{
			"code":       code,
			"expires_in": "5m",
		})
	}
}

func TelegramLinkStatus(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, err := GetUserFromRequest(r, pool)
		if err != nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}

		var tgID *int64
		var tgUsername *string
		err = pool.QueryRow(r.Context(), `
			SELECT telegram_id, telegram_username FROM telegram_links WHERE user_id = $1
		`, userID).Scan(&tgID, &tgUsername)

		json.NewEncoder(w).Encode(map[string]interface{}{
			"telegram_id":       tgID,
			"telegram_username": tgUsername,
		})
	}
}

func RequireAuth(pool *pgxpool.Pool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, err := GetUserFromRequest(r, pool)
			if err != nil {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func NotificationsList(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, err := GetUserFromRequest(r, pool)
		if err != nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}

		rows, err := pool.Query(r.Context(), `
			SELECT id, tracker_id, type, old_price, new_price, currency,
			       old_stock_status, new_stock_status, status, created_at
			FROM notifications
			WHERE user_id = $1
			ORDER BY created_at DESC
			LIMIT 50
		`, userID)
		if err != nil {
			http.Error(w, "failed to load notifications", http.StatusInternalServerError)
			return
		}
		defer rows.Close()

		type Notification struct {
			ID             string     `json:"id"`
			TrackerID      string     `json:"tracker_id"`
			Type           string     `json:"type"`
			OldPrice       *float64   `json:"old_price"`
			NewPrice       *float64   `json:"new_price"`
			Currency       *string    `json:"currency"`
			OldStockStatus *string    `json:"old_stock_status"`
			NewStockStatus *string    `json:"new_stock_status"`
			Status         string     `json:"status"`
			CreatedAt      time.Time  `json:"created_at"`
		}

		var notifications []Notification
		for rows.Next() {
			var n Notification
			if err := rows.Scan(&n.ID, &n.TrackerID, &n.Type, &n.OldPrice, &n.NewPrice,
				&n.Currency, &n.OldStockStatus, &n.NewStockStatus, &n.Status, &n.CreatedAt); err != nil {
				continue
			}
			notifications = append(notifications, n)
		}

		if notifications == nil {
			notifications = []Notification{}
		}

		json.NewEncoder(w).Encode(notifications)
	}
}
