package handler

import (
	"encoding/json"
	"net/http"
	"net/url"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/littlewell/price-tracker/internal/config"
	"github.com/littlewell/price-tracker/internal/security"
)

func ListTrackers(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, err := GetUserFromRequest(r, pool)
		if err != nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}

		rows, err := pool.Query(r.Context(), `
			SELECT id, url, domain, title, image_url, current_price, currency,
			       current_stock_status, status, last_checked_at, next_check_at, created_at
			FROM trackers WHERE user_id = $1 AND status != 'deleted'
			ORDER BY created_at DESC
		`, userID)
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		defer rows.Close()

		type Tracker struct {
			ID                 uuid.UUID  `json:"id"`
			URL                string     `json:"url"`
			Domain             string     `json:"domain"`
			Title              *string    `json:"title"`
			ImageURL           *string    `json:"image_url"`
			CurrentPrice       *float64   `json:"current_price"`
			Currency           string     `json:"currency"`
			CurrentStockStatus string     `json:"current_stock_status"`
			Status             string     `json:"status"`
			LastCheckedAt      *time.Time `json:"last_checked_at"`
			NextCheckAt        time.Time  `json:"next_check_at"`
			CreatedAt          time.Time  `json:"created_at"`
		}

		var trackers []Tracker
		for rows.Next() {
			var t Tracker
			if err := rows.Scan(&t.ID, &t.URL, &t.Domain, &t.Title, &t.ImageURL,
				&t.CurrentPrice, &t.Currency, &t.CurrentStockStatus,
				&t.Status, &t.LastCheckedAt, &t.NextCheckAt, &t.CreatedAt); err != nil {
				continue
			}
			trackers = append(trackers, t)
		}

		if trackers == nil {
			trackers = []Tracker{}
		}

		json.NewEncoder(w).Encode(trackers)
	}
}

func GetTracker(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, err := GetUserFromRequest(r, pool)
		if err != nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}

		id := 	chi.URLParam(r, "id")
		if id == "" {
			id = r.URL.Query().Get("id")
		}

		var t struct {
			ID                  uuid.UUID  `json:"id"`
			URL                 string     `json:"url"`
			Domain              string     `json:"domain"`
			Title               *string    `json:"title"`
			ImageURL            *string    `json:"image_url"`
			InitialPrice        float64    `json:"initial_price"`
			CurrentPrice        *float64   `json:"current_price"`
			PreviousPrice       *float64   `json:"previous_price"`
			Currency            string     `json:"currency"`
			CurrentStockStatus  string     `json:"current_stock_status"`
			PreviousStockStatus *string    `json:"previous_stock_status"`
			Status              string     `json:"status"`
			LastCheckedAt       *time.Time `json:"last_checked_at"`
			NextCheckAt         time.Time  `json:"next_check_at"`
			LastError           *string    `json:"last_error"`
			ConsecutiveErrors   int        `json:"consecutive_errors"`
			CreatedAt           time.Time  `json:"created_at"`
		}
		err = pool.QueryRow(r.Context(), `
			SELECT id, url, domain, title, image_url, initial_price, current_price, previous_price,
			       currency, current_stock_status, previous_stock_status, status,
			       last_checked_at, next_check_at, last_error, consecutive_errors, created_at
			FROM trackers WHERE id = $1 AND user_id = $2 AND status != 'deleted'
		`, id, userID).Scan(
			&t.ID, &t.URL, &t.Domain, &t.Title, &t.ImageURL,
			&t.InitialPrice, &t.CurrentPrice, &t.PreviousPrice,
			&t.Currency, &t.CurrentStockStatus, &t.PreviousStockStatus,
			&t.Status, &t.LastCheckedAt, &t.NextCheckAt, &t.LastError,
			&t.ConsecutiveErrors, &t.CreatedAt,
		)
		if err != nil {
			http.Error(w, "tracker not found", http.StatusNotFound)
			return
		}
		json.NewEncoder(w).Encode(t)
	}
}

func CreateTracker(pool *pgxpool.Pool, cfg *config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, err := GetUserFromRequest(r, pool)
		if err != nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}

		var req struct {
			URL          string  `json:"url"`
			InitialPrice float64 `json:"initial_price"`
			Currency     string  `json:"currency"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid body", http.StatusBadRequest)
			return
		}

		if err := security.ValidateURL(req.URL); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		var maxTrackers int
		err = pool.QueryRow(r.Context(), `
			SELECT COALESCE(p.max_trackers, 5) FROM user_plans up
			JOIN plans p ON up.plan_code = p.code
			WHERE up.user_id = $1
		`, userID).Scan(&maxTrackers)
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}

		var activeCount int
		pool.QueryRow(r.Context(), `
			SELECT COUNT(*) FROM trackers WHERE user_id = $1 AND status = 'active'
		`, userID).Scan(&activeCount)

		if activeCount >= maxTrackers {
			http.Error(w, "tracker limit reached", http.StatusForbidden)
			return
		}

		u, _ := url.Parse(req.URL)

		id := uuid.New()
		_, err = pool.Exec(r.Context(), `
			INSERT INTO trackers (id, user_id, url, normalized_url, domain, initial_price, currency, status)
			VALUES ($1, $2, $3, $4, $5, $6, $7, 'needs_confirmation')
		`, id, userID, req.URL, req.URL, u.Hostname(), req.InitialPrice, req.Currency)
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}

		json.NewEncoder(w).Encode(map[string]interface{}{
			"id":     id,
			"status": "needs_confirmation",
		})
	}
}

func UpdateTracker(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, err := GetUserFromRequest(r, pool)
		if err != nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}

		id := 	chi.URLParam(r, "id")
		if id == "" {
			id = r.URL.Query().Get("id")
		}

		var req struct {
			Title *string `json:"title"`
		}
		json.NewDecoder(r.Body).Decode(&req)

		if req.Title != nil {
			pool.Exec(r.Context(), `UPDATE trackers SET title = $1, updated_at = now() WHERE id = $2 AND user_id = $3`, *req.Title, id, userID)
		}

		json.NewEncoder(w).Encode(map[string]string{"status": "updated"})
	}
}

func DeleteTracker(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, err := GetUserFromRequest(r, pool)
		if err != nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}

		id := 	chi.URLParam(r, "id")
		if id == "" {
			id = r.URL.Query().Get("id")
		}

		_, err = pool.Exec(r.Context(), `UPDATE trackers SET status = 'deleted', updated_at = now() WHERE id = $1 AND user_id = $2`, id, userID)
		if err != nil {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		json.NewEncoder(w).Encode(map[string]string{"status": "deleted"})
	}
}

func PauseTracker(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, err := GetUserFromRequest(r, pool)
		if err != nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		id := 	chi.URLParam(r, "id")
		if id == "" {
			id = r.URL.Query().Get("id")
		}
		_, err = pool.Exec(r.Context(), `UPDATE trackers SET status = 'paused', updated_at = now() WHERE id = $1 AND user_id = $2`, id, userID)
		if err != nil {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		json.NewEncoder(w).Encode(map[string]string{"status": "paused"})
	}
}

func ResumeTracker(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, err := GetUserFromRequest(r, pool)
		if err != nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		id := 	chi.URLParam(r, "id")
		if id == "" {
			id = r.URL.Query().Get("id")
		}
		_, err = pool.Exec(r.Context(), `UPDATE trackers SET status = 'active', updated_at = now() WHERE id = $1 AND user_id = $2`, id, userID)
		if err != nil {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		json.NewEncoder(w).Encode(map[string]string{"status": "active"})
	}
}

func RecheckTracker(pool *pgxpool.Pool, cfg *config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, err := GetUserFromRequest(r, pool)
		if err != nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		id := 	chi.URLParam(r, "id")
		if id == "" {
			id = r.URL.Query().Get("id")
		}
		_, err = pool.Exec(r.Context(), `UPDATE trackers SET next_check_at = now(), updated_at = now() WHERE id = $1 AND user_id = $2`, id, userID)
		if err != nil {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		json.NewEncoder(w).Encode(map[string]string{"status": "queued"})
	}
}

func TrackerHistory(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, err := GetUserFromRequest(r, pool)
		if err != nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}

		id := 	chi.URLParam(r, "id")
		if id == "" {
			id = r.URL.Query().Get("id")
		}

		var ownerID string
		err = pool.QueryRow(r.Context(), `SELECT user_id::text FROM trackers WHERE id = $1`, id).Scan(&ownerID)
		if err != nil || ownerID != userID {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}

		priceRows, _ := pool.Query(r.Context(), `
			SELECT price, currency, source, status, error_message, checked_at
			FROM price_points WHERE tracker_id = $1 ORDER BY checked_at DESC LIMIT 100
		`, id)

		type PricePoint struct {
			Price        *float64  `json:"price"`
			Currency     string    `json:"currency"`
			Source       string    `json:"source"`
			Status       string    `json:"status"`
			ErrorMessage *string   `json:"error_message"`
			CheckedAt    time.Time `json:"checked_at"`
		}

		var prices []PricePoint
		if priceRows != nil {
			defer priceRows.Close()
			for priceRows.Next() {
				var p PricePoint
				if err := priceRows.Scan(&p.Price, &p.Currency, &p.Source, &p.Status, &p.ErrorMessage, &p.CheckedAt); err == nil {
					prices = append(prices, p)
				}
			}
		}

		stockRows, _ := pool.Query(r.Context(), `
			SELECT stock_status, source, status, error_message, checked_at
			FROM stock_points WHERE tracker_id = $1 ORDER BY checked_at DESC LIMIT 100
		`, id)

		type StockPoint struct {
			StockStatus  string    `json:"stock_status"`
			Source       string    `json:"source"`
			Status       string    `json:"status"`
			ErrorMessage *string   `json:"error_message"`
			CheckedAt    time.Time `json:"checked_at"`
		}

		var stocks []StockPoint
		if stockRows != nil {
			defer stockRows.Close()
			for stockRows.Next() {
				var s StockPoint
				if err := stockRows.Scan(&s.StockStatus, &s.Source, &s.Status, &s.ErrorMessage, &s.CheckedAt); err == nil {
					stocks = append(stocks, s)
				}
			}
		}

		if prices == nil {
			prices = []PricePoint{}
		}
		if stocks == nil {
			stocks = []StockPoint{}
		}

		json.NewEncoder(w).Encode(map[string]interface{}{
			"prices": prices,
			"stocks": stocks,
		})
	}
}
