package handler

import (
	"context"

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
var freePlanFallback = planLimits{code: "free", maxTrackers: 3, minIntervalMinutes: 180}

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

// countActiveTrackers counts the trackers that occupy a plan slot (everything not deleted,
// matching what /list shows).
func countActiveTrackers(ctx context.Context, pool *pgxpool.Pool, userID string) (int, error) {
	var n int
	err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM trackers WHERE user_id = $1 AND status != 'deleted'`, userID).Scan(&n)
	return n, err
}
