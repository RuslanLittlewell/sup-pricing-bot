package main

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/littlewell/price-tracker/internal/db"
	"github.com/rs/zerolog"
)

// newTrialTestPool connects to a scratch Postgres and runs the real migrations against it.
// Skipped unless TEST_DATABASE_URL is set, so the default `go test ./...` stays hermetic:
//
//	docker run -d --name pct-test-db -e POSTGRES_PASSWORD=postgres \
//	    -e POSTGRES_DB=price_tracker -p 55433:5432 postgres:16-alpine
//	TEST_DATABASE_URL=postgres://postgres:postgres@localhost:55433/price_tracker go test ./cmd/worker/
func newTrialTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	log := zerolog.Nop()
	pool, err := db.ConnectPool(ctx, dsn, log)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	if err := db.RunMigrations(ctx, pool, log); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	// Each test starts from a clean slate; the tables are scratch data only.
	for _, table := range []string{"notifications", "price_points", "stock_points", "trackers", "user_plans", `"user"`} {
		if _, err := pool.Exec(ctx, "DELETE FROM "+table); err != nil {
			t.Fatalf("clean %s: %v", table, err)
		}
	}
	t.Cleanup(pool.Close)
	return pool
}

// seedUser creates the "user" row that user_plans and trackers both key off.
func seedUser(t *testing.T, pool *pgxpool.Pool, userID string) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO "user" (id, name, email) VALUES ($1, $1, $1 || '@telegram.local')
		ON CONFLICT (id) DO NOTHING
	`, userID); err != nil {
		t.Fatalf("seed user: %v", err)
	}
}

func seedTrialUser(t *testing.T, pool *pgxpool.Pool, userID string, expiresAt time.Time, trackerTitles ...string) {
	t.Helper()
	ctx := context.Background()
	seedUser(t, pool, userID)
	if _, err := pool.Exec(ctx, `
		INSERT INTO user_plans (user_id, plan_code, status, expires_at, trial_used)
		VALUES ($1, 'trial', 'active', $2, true)
	`, userID, expiresAt); err != nil {
		t.Fatalf("seed plan: %v", err)
	}
	// Each successive tracker is created later than the last, so "oldest first" ordering
	// in the job under test is unambiguous.
	for i, title := range trackerTitles {
		createdAt := time.Now().Add(-time.Duration(len(trackerTitles)-i) * 24 * time.Hour)
		if _, err := pool.Exec(ctx, `
			INSERT INTO trackers (user_id, url, normalized_url, domain, title, initial_price,
			                      current_price, currency, status, created_at)
			VALUES ($1, $2, $2, 'shop.example', $3, 100, 100, 'PLN', 'active', $4)
		`, userID, "https://shop.example/p/"+title, title, createdAt); err != nil {
			t.Fatalf("seed tracker %s: %v", title, err)
		}
	}
}

func activeTrackerTitles(t *testing.T, pool *pgxpool.Pool, userID string) []string {
	t.Helper()
	rows, err := pool.Query(context.Background(),
		`SELECT title FROM trackers WHERE user_id = $1 AND status = 'active' ORDER BY created_at, id`, userID)
	if err != nil {
		t.Fatalf("query active: %v", err)
	}
	defer rows.Close()
	var titles []string
	for rows.Next() {
		var title string
		if err := rows.Scan(&title); err != nil {
			t.Fatal(err)
		}
		titles = append(titles, title)
	}
	return titles
}

func planCodeOf(t *testing.T, pool *pgxpool.Pool, userID string) (code, status string) {
	t.Helper()
	if err := pool.QueryRow(context.Background(),
		`SELECT plan_code, status FROM user_plans WHERE user_id = $1`, userID).Scan(&code, &status); err != nil {
		t.Fatalf("read plan: %v", err)
	}
	return code, status
}

func TestExpireTrialsDowngradesAndCompletesSurplus(t *testing.T) {
	pool := newTrialTestPool(t)
	ctx := context.Background()

	// 7 active trackers, mirroring the real user this was written for. Free allows 3.
	seedTrialUser(t, pool, "tg_expired", time.Now().Add(-time.Hour),
		"a-oldest", "b", "c", "d", "e", "f", "g-newest")

	expireTrials(ctx, pool, zerolog.Nop())

	code, status := planCodeOf(t, pool, "tg_expired")
	if code != "free" || status != "expired" {
		t.Fatalf("plan = %s/%s, want free/expired", code, status)
	}

	got := activeTrackerTitles(t, pool, "tg_expired")
	want := []string{"a-oldest", "b", "c"}
	if len(got) != len(want) {
		t.Fatalf("active trackers = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("active trackers = %v, want %v (oldest are kept)", got, want)
		}
	}

	var completed int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM trackers
		WHERE user_id = 'tg_expired' AND status = 'completed' AND completed_at IS NOT NULL`).Scan(&completed); err != nil {
		t.Fatal(err)
	}
	if completed != 4 {
		t.Fatalf("completed = %d, want 4", completed)
	}

	var notifs int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM notifications
		WHERE type = 'trial_expired' AND status = 'pending'`).Scan(&notifs); err != nil {
		t.Fatal(err)
	}
	if notifs != 4 {
		t.Fatalf("queued notifications = %d, want 4 (one per completed tracker)", notifs)
	}
}

func TestExpireTrialsIsIdempotent(t *testing.T) {
	pool := newTrialTestPool(t)
	ctx := context.Background()
	seedTrialUser(t, pool, "tg_twice", time.Now().Add(-time.Hour), "a", "b", "c", "d", "e")

	expireTrials(ctx, pool, zerolog.Nop())
	expireTrials(ctx, pool, zerolog.Nop())
	expireTrials(ctx, pool, zerolog.Nop())

	if got := len(activeTrackerTitles(t, pool, "tg_twice")); got != 3 {
		t.Fatalf("active = %d, want 3", got)
	}
	var notifs int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM notifications WHERE type = 'trial_expired'`).Scan(&notifs); err != nil {
		t.Fatal(err)
	}
	// A second sweep must not re-notify: the plan is no longer 'trial', so nothing matches.
	if notifs != 2 {
		t.Fatalf("notifications = %d, want 2 — repeat runs must not re-notify", notifs)
	}
}

func TestExpireTrialsLeavesLiveTrialAlone(t *testing.T) {
	pool := newTrialTestPool(t)
	seedTrialUser(t, pool, "tg_live", time.Now().Add(72*time.Hour), "a", "b", "c", "d", "e")

	expireTrials(context.Background(), pool, zerolog.Nop())

	if code, status := planCodeOf(t, pool, "tg_live"); code != "trial" || status != "active" {
		t.Fatalf("plan = %s/%s, want trial/active", code, status)
	}
	if got := len(activeTrackerTitles(t, pool, "tg_live")); got != 5 {
		t.Fatalf("active = %d, want 5 — a running trial keeps its allowance", got)
	}
}

func TestExpireTrialsLeavesLapsedPaidPlanAlone(t *testing.T) {
	pool := newTrialTestPool(t)
	ctx := context.Background()
	seedUser(t, pool, "tg_paid")
	if _, err := pool.Exec(ctx, `
		INSERT INTO user_plans (user_id, plan_code, status, expires_at, trial_used)
		VALUES ('tg_paid', 'pro', 'active', now() - interval '1 hour', true)
	`); err != nil {
		t.Fatal(err)
	}
	seedTrialUser(t, pool, "tg_other", time.Now().Add(-time.Hour), "x", "y", "z", "w")

	expireTrials(ctx, pool, zerolog.Nop())

	// A lapsed paid plan may just be a webhook running late — never complete its trackers.
	if code, _ := planCodeOf(t, pool, "tg_paid"); code != "pro" {
		t.Fatalf("paid plan_code = %s, want pro (untouched)", code)
	}
	if code, _ := planCodeOf(t, pool, "tg_other"); code != "free" {
		t.Fatalf("expired trial was not downgraded: %s", code)
	}
}

func TestExpireTrialsKeepsTrialUsedSoItCannotRestart(t *testing.T) {
	pool := newTrialTestPool(t)
	seedTrialUser(t, pool, "tg_used", time.Now().Add(-time.Hour), "a")

	expireTrials(context.Background(), pool, zerolog.Nop())

	var used bool
	if err := pool.QueryRow(context.Background(),
		`SELECT trial_used FROM user_plans WHERE user_id = 'tg_used'`).Scan(&used); err != nil {
		t.Fatal(err)
	}
	if !used {
		t.Fatal("trial_used must survive the downgrade, otherwise the trial can be re-activated")
	}
}

func TestExpireTrialsUnderLimitCompletesNothing(t *testing.T) {
	pool := newTrialTestPool(t)
	seedTrialUser(t, pool, "tg_small", time.Now().Add(-time.Hour), "a", "b")

	expireTrials(context.Background(), pool, zerolog.Nop())

	if got := len(activeTrackerTitles(t, pool, "tg_small")); got != 2 {
		t.Fatalf("active = %d, want 2 — nothing to trim under the limit", got)
	}
	var notifs int
	if err := pool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM notifications WHERE type = 'trial_expired'`).Scan(&notifs); err != nil {
		t.Fatal(err)
	}
	if notifs != 0 {
		t.Fatalf("notifications = %d, want 0 — no tracker was stopped", notifs)
	}
}
