package handler

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// planLimits are the enforceable quotas of a user's effective plan.
type planLimits struct {
	code               string
	maxTrackers        int
	minIntervalMinutes int
}

// freePlanFallback mirrors the 'free' row seeded by migration V10. Used only if the
// plans table can't be read, so limits still apply instead of silently going unlimited.
var freePlanFallback = planLimits{code: "free", maxTrackers: 3, minIntervalMinutes: 300}

// getPlanLimits resolves the user's *effective* plan limits. A paid plan applies while
// its subscription hasn't expired; a cancelled-but-not-yet-expired subscription keeps
// access for the period already paid for (we key off expires_at, not status). Anything
// expired or unset falls back to Free.
func getPlanLimits(ctx context.Context, pool *pgxpool.Pool, userID string) planLimits {
	var pl planLimits
	err := pool.QueryRow(ctx, `
		SELECT p.code, p.max_trackers, p.check_interval_minutes
		FROM plans p
		WHERE p.code = COALESCE((
			SELECT up.plan_code FROM user_plans up
			WHERE up.user_id = $1 AND (up.expires_at IS NULL OR up.expires_at > now())
			LIMIT 1
		), 'free')
	`, userID).Scan(&pl.code, &pl.maxTrackers, &pl.minIntervalMinutes)
	if err != nil {
		return freePlanFallback
	}
	return pl
}

// countActiveTrackers counts only trackers that still occupy a plan slot. Completed
// trackers remain as history but no longer consume the user's active-tracker allowance.
func countActiveTrackers(ctx context.Context, pool *pgxpool.Pool, userID string) (int, error) {
	var n int
	err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM trackers WHERE user_id = $1 AND status = 'active'`, userID).Scan(&n)
	return n, err
}

// hasUsedTrial reports whether this user has already activated their one-time trial —
// a user with no user_plans row yet has never used it.
func hasUsedTrial(ctx context.Context, pool *pgxpool.Pool, userID string) bool {
	var used bool
	err := pool.QueryRow(ctx, `SELECT trial_used FROM user_plans WHERE user_id = $1`, userID).Scan(&used)
	if err != nil {
		return false
	}
	return used
}

// activateTrial upgrades the user to Trial (Basic-equivalent limits for trialDuration),
// but only the first time — trial_used = false is enforced in the UPDATE's WHERE clause so
// a user can never re-activate it, even after their trial plan has since expired back to
// Free. Returns ok=false (no error) when the trial was already used.
func activateTrial(ctx context.Context, pool *pgxpool.Pool, userID string) (ok bool, err error) {
	var returnedID string
	err = pool.QueryRow(ctx, `
		INSERT INTO user_plans (user_id, plan_code, status, expires_at, trial_used)
		VALUES ($1, 'trial', 'active', now() + interval '14 days', true)
		ON CONFLICT (user_id) DO UPDATE SET
			plan_code = 'trial',
			status = 'active',
			expires_at = now() + interval '14 days',
			trial_used = true,
			updated_at = now()
		WHERE user_plans.trial_used = false
		RETURNING user_id
	`, userID).Scan(&returnedID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}
