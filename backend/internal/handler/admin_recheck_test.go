package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"

	"github.com/littlewell/price-tracker/internal/db"
)

// Skipped unless TEST_DATABASE_URL points at a scratch Postgres (see
// cmd/worker/trial_expiry_test.go for a one-line container to run).
func newRecheckTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	pool, err := db.ConnectPool(ctx, dsn, zerolog.Nop())
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	if err := db.RunMigrations(ctx, pool, zerolog.Nop()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	for _, table := range []string{"notifications", "price_points", "stock_points", "trackers", "user_plans", `"user"`} {
		if _, err := pool.Exec(ctx, "DELETE FROM "+table); err != nil {
			t.Fatalf("clean %s: %v", table, err)
		}
	}
	if _, err := pool.Exec(ctx, `INSERT INTO "user" (id, name, email) VALUES ('u1', 'u1', 'u1@telegram.local')`); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func seedProblemTracker(t *testing.T, pool *pgxpool.Pool, status string) string {
	t.Helper()
	var id string
	if err := pool.QueryRow(context.Background(), `
		INSERT INTO trackers (user_id, url, normalized_url, domain, title, initial_price, current_price,
		                      currency, status, consecutive_errors, last_error, last_checked_at, next_check_at)
		VALUES ('u1', 'https://shop.example/p/1', 'https://shop.example/p/1', 'shop.example', 'Item', 100, 100,
		        'PLN', $1, 3, 'page is blocked by anti-bot protection', now() - interval '1 hour', now() + interval '1 day')
		RETURNING id::text
	`, status).Scan(&id); err != nil {
		t.Fatalf("seed tracker: %v", err)
	}
	return id
}

func postRecheck(t *testing.T, pool *pgxpool.Pool, id string) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	r := chi.NewRouter()
	r.Post("/trackers/{id}/recheck", AdminRecheckTracker(pool, zerolog.Nop()))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/trackers/"+id+"/recheck", nil))
	var body map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	return rec, body
}

func TestAdminRecheckQueuesParkedTrackerForTheWorker(t *testing.T) {
	pool := newRecheckTestPool(t)
	id := seedProblemTracker(t, pool, "needs_confirmation")

	var before string
	_ = pool.QueryRow(context.Background(), `SELECT last_checked_at::text FROM trackers WHERE id = $1`, id).Scan(&before)

	rec, body := postRecheck(t, pool, id)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	if body["lastCheckedAt"] != before {
		t.Fatalf("lastCheckedAt = %v, want %q — the dashboard compares it to spot the worker's run", body["lastCheckedAt"], before)
	}

	var status, lastError string
	var errorsCount int
	var due, manual bool
	if err := pool.QueryRow(context.Background(), `
		SELECT status, consecutive_errors, next_check_at <= now(), manual_check_pending, last_error
		FROM trackers WHERE id = $1
	`, id).Scan(&status, &errorsCount, &due, &manual, &lastError); err != nil {
		t.Fatal(err)
	}
	if status != "active" || errorsCount != 0 || !due {
		t.Fatalf("status=%s errors=%d due=%v: the worker only picks up active, due trackers", status, errorsCount, due)
	}
	if manual {
		t.Fatal("an admin recheck must not be a manual check — that reports the outcome to the owner")
	}
	if lastError == "" {
		t.Fatal("the old error must stay until the worker has actually rechecked")
	}
}

func TestAdminRecheckRefusesTrackersOutOfPlay(t *testing.T) {
	pool := newRecheckTestPool(t)
	id := seedProblemTracker(t, pool, "expired")

	if rec, body := postRecheck(t, pool, id); rec.Code != http.StatusConflict || body["error"] == nil {
		t.Fatalf("an expired tracker must not be revived: %d %s", rec.Code, rec.Body)
	}
	var status string
	_ = pool.QueryRow(context.Background(), `SELECT status FROM trackers WHERE id = $1`, id).Scan(&status)
	if status != "expired" {
		t.Fatalf("status changed to %s", status)
	}

	if rec, _ := postRecheck(t, pool, "00000000-0000-0000-0000-000000000000"); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown tracker: got %d", rec.Code)
	}
}
