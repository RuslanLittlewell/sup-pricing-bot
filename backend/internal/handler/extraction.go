package handler

import (
	"encoding/json"
	"net/http"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"

	"github.com/littlewell/price-tracker/internal/config"
	"github.com/littlewell/price-tracker/internal/extractor"
	"github.com/littlewell/price-tracker/internal/renderer"
)

func ExtractPreview(pool *pgxpool.Pool, cfg *config.Config, log zerolog.Logger, rend *renderer.Renderer) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			URL          string  `json:"url"`
			InitialPrice float64 `json:"initial_price"`
			Currency     string  `json:"currency"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid body", http.StatusBadRequest)
			return
		}

		fetcher := extractor.NewPageFetcher(rend, cfg.ScraperCookies, cfg.ScraperProxy)
		body, err := fetcher.Fetch(req.URL)
		if err != nil {
			http.Error(w, "failed to fetch page: "+err.Error(), http.StatusBadRequest)
			return
		}

		// Try Zara extractor first, fall back to generic
		zara := extractor.NewZara()
		result, err := zara.Extract(body, req.URL)
		if err != nil || len(result.Candidates) == 0 {
			generic := extractor.NewGeneric()
			result, err = generic.Extract(body, req.URL)
		}

		if err != nil {
			http.Error(w, "extraction failed", http.StatusInternalServerError)
			return
		}

		json.NewEncoder(w).Encode(result)
	}
}

func ExtractConfirm(pool *pgxpool.Pool, cfg *config.Config, log zerolog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			TrackerID            uuid.UUID       `json:"tracker_id"`
			SelectedPrice        string          `json:"selected_price"`
			SelectedCurrency     string          `json:"selected_currency"`
			ExtractionRule       json.RawMessage `json:"extraction_rule"`
			ExtractionConfidence float64         `json:"extraction_confidence"`
			Title                string          `json:"title"`
			ImageURL             string          `json:"image_url"`
			StockStatus          string          `json:"stock_status"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid body", http.StatusBadRequest)
			return
		}

		_, err := pool.Exec(r.Context(), `
			UPDATE trackers SET
				status = 'active',
				title = $2,
				image_url = $3,
				current_price = $4,
				extraction_rule = $5,
				extraction_confidence = $6,
				current_stock_status = $7,
				updated_at = now(),
				next_check_at = now()
			WHERE id = $1 AND status = 'needs_confirmation'
		`, req.TrackerID, req.Title, req.ImageURL, req.SelectedPrice,
			req.ExtractionRule, req.ExtractionConfidence, req.StockStatus)
		if err != nil {
			http.Error(w, "confirmation failed", http.StatusInternalServerError)
			return
		}

		// Save initial price point
		ppID := uuid.New()
		pool.Exec(r.Context(), `
			INSERT INTO price_points (id, tracker_id, price, currency, source, status)
			VALUES ($1, $2, $3, $4, 'confirmed_by_user', 'success')
		`, ppID, req.TrackerID, req.SelectedPrice, req.SelectedCurrency)

		// Save initial stock point
		spID := uuid.New()
		pool.Exec(r.Context(), `
			INSERT INTO stock_points (id, tracker_id, stock_status, source, status)
			VALUES ($1, $2, $3, 'confirmed_by_user', 'success')
		`, spID, req.TrackerID, req.StockStatus)

		json.NewEncoder(w).Encode(map[string]string{"status": "active"})
	}
}
