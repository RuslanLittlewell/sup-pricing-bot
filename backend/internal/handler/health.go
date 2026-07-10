package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func Livez(w http.ResponseWriter, r *http.Request) {
	writeHealth(w, http.StatusOK, map[string]any{"status": "ok", "service": "api"})
}

func Healthz(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()

		response := map[string]any{"status": "ok", "database": "ok", "worker": "ok"}
		if err := pool.Ping(ctx); err != nil {
			response["status"], response["database"] = "unhealthy", "unavailable"
			writeHealth(w, http.StatusServiceUnavailable, response)
			return
		}

		var lastSeen time.Time
		if err := pool.QueryRow(ctx, `SELECT last_seen_at FROM service_heartbeats WHERE service = 'worker'`).Scan(&lastSeen); err != nil {
			response["status"], response["worker"] = "unhealthy", "missing"
			writeHealth(w, http.StatusServiceUnavailable, response)
			return
		}
		age := time.Since(lastSeen)
		response["workerLastSeenAt"] = lastSeen.UTC().Format(time.RFC3339)
		response["workerAgeSeconds"] = int(age.Seconds())
		if age > 90*time.Second {
			response["status"], response["worker"] = "unhealthy", "stale"
			writeHealth(w, http.StatusServiceUnavailable, response)
			return
		}
		writeHealth(w, http.StatusOK, response)
	}
}

func writeHealth(w http.ResponseWriter, status int, body map[string]any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
