package db

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"
)

type Migration struct {
	Version     int
	Description string
	SQL         string
}

var Migrations = []Migration{
	{
		Version:     1,
		Description: "create legacy auth and app tables",
		SQL:         migrationV1,
	},
	{
		Version:     2,
		Description: "seed plans",
		SQL:         migrationV2,
	},
	{
		Version:     3,
		Description: "migrate to Better Auth tables, add telegram_links",
		SQL:         migrationV3,
	},
	{
		Version:     4,
		Description: "add telegram bot dialog states",
		SQL:         migrationV4,
	},
	{
		Version:     5,
		Description: "add currency to telegram bot states",
		SQL:         migrationV5,
	},
	{
		Version:     6,
		Description: "add per tracker check interval",
		SQL:         migrationV6,
	},
	{
		Version:     7,
		Description: "add telegram language preference",
		SQL:         migrationV7,
	},
	{
		Version:     8,
		Description: "add product title to telegram bot states",
		SQL:         migrationV8,
	},
	{
		Version:     9,
		Description: "add tracking mode (price or stock) to trackers",
		SQL:         migrationV9,
	},
	{
		Version:     10,
		Description: "add basic plan, align plan limits, add Tribute subscription tracking",
		SQL:         migrationV10,
	},
	{
		Version:     11,
		Description: "track which extraction method resolved each price point",
		SQL:         migrationV11,
	},
	{
		Version:     12,
		Description: "log price extraction failures that happen before a tracker exists",
		SQL:         migrationV12,
	},
	{
		Version:     13,
		Description: "track which method resolved each stock check",
		SQL:         migrationV13,
	},
	{
		Version:     14,
		Description: "add proxy pool table",
		SQL:         migrationV14,
	},
	{
		Version:     15,
		Description: "support authenticated proxies and track proxy source",
		SQL:         migrationV15,
	},
	{
		Version:     16,
		Description: "track which fetch tier (direct/cf_relay/render) produced each check",
		SQL:         migrationV16,
	},
	{
		Version:     17,
		Description: "add one-time trial plan",
		SQL:         migrationV17,
	},
}

const migrationV1 = `
CREATE EXTENSION IF NOT EXISTS "pgcrypto";

CREATE TABLE IF NOT EXISTS users (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    telegram_user_id BIGINT UNIQUE NOT NULL,
    telegram_username TEXT,
    telegram_first_name TEXT,
    telegram_last_name TEXT,
    language_code TEXT NOT NULL DEFAULT 'ru',
    plan_code TEXT NOT NULL DEFAULT 'free',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS login_challenges (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    token TEXT UNIQUE NOT NULL,
    telegram_user_id BIGINT,
    status TEXT NOT NULL DEFAULT 'pending',
    expires_at TIMESTAMPTZ NOT NULL,
    consumed_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS sessions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_hash TEXT UNIQUE NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS plans (
    code TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    max_trackers INT NOT NULL,
    check_interval_minutes INT NOT NULL,
    price_history_days INT NOT NULL,
    is_paid BOOLEAN NOT NULL DEFAULT false
);

CREATE TABLE IF NOT EXISTS trackers (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    url TEXT NOT NULL,
    normalized_url TEXT NOT NULL,
    domain TEXT NOT NULL,
    title TEXT,
    image_url TEXT,
    initial_price NUMERIC(14, 2) NOT NULL,
    current_price NUMERIC(14, 2),
    previous_price NUMERIC(14, 2),
    currency TEXT NOT NULL,
    current_stock_status TEXT NOT NULL DEFAULT 'unknown',
    previous_stock_status TEXT,
    extraction_rule JSONB,
    extraction_confidence NUMERIC(5, 2),
    status TEXT NOT NULL DEFAULT 'active',
    last_checked_at TIMESTAMPTZ,
    next_check_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_error TEXT,
    consecutive_errors INT NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS price_points (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tracker_id UUID NOT NULL REFERENCES trackers(id) ON DELETE CASCADE,
    price NUMERIC(14, 2),
    currency TEXT NOT NULL,
    source TEXT NOT NULL,
    status TEXT NOT NULL,
    error_message TEXT,
    checked_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS stock_points (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tracker_id UUID NOT NULL REFERENCES trackers(id) ON DELETE CASCADE,
    stock_status TEXT NOT NULL,
    source TEXT NOT NULL,
    status TEXT NOT NULL,
    error_message TEXT,
    checked_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS notifications (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    tracker_id UUID NOT NULL REFERENCES trackers(id) ON DELETE CASCADE,
    type TEXT NOT NULL,
    old_price NUMERIC(14, 2),
    new_price NUMERIC(14, 2),
    currency TEXT,
    old_stock_status TEXT,
    new_stock_status TEXT,
    telegram_message_id BIGINT,
    status TEXT NOT NULL DEFAULT 'pending',
    error_message TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    sent_at TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_sessions_token_hash ON sessions(token_hash);
CREATE INDEX IF NOT EXISTS idx_sessions_user_id ON sessions(user_id);
CREATE INDEX IF NOT EXISTS idx_login_challenges_token ON login_challenges(token);
CREATE INDEX IF NOT EXISTS idx_trackers_user_id ON trackers(user_id);
CREATE INDEX IF NOT EXISTS idx_trackers_status_next_check ON trackers(status, next_check_at);
CREATE INDEX IF NOT EXISTS idx_price_points_tracker_id ON price_points(tracker_id);
CREATE INDEX IF NOT EXISTS idx_stock_points_tracker_id ON stock_points(tracker_id);
CREATE INDEX IF NOT EXISTS idx_notifications_tracker_id ON notifications(tracker_id);
CREATE INDEX IF NOT EXISTS idx_notifications_user_id ON notifications(user_id);
`

const migrationV2 = `
INSERT INTO plans (code, name, max_trackers, check_interval_minutes, price_history_days, is_paid)
VALUES
    ('free', 'Free', 5, 60, 30, false),
    ('pro', 'Pro', 100, 15, 365, true)
ON CONFLICT (code) DO NOTHING;
`

const migrationV3 = `
-- Drop old FK constraints
ALTER TABLE trackers DROP CONSTRAINT IF EXISTS trackers_user_id_fkey;
ALTER TABLE notifications DROP CONSTRAINT IF EXISTS notifications_user_id_fkey;

-- Drop old auth tables
DROP TABLE IF EXISTS sessions CASCADE;
DROP TABLE IF EXISTS login_challenges CASCADE;
DROP TABLE IF EXISTS users CASCADE;

-- Better Auth user table
CREATE TABLE IF NOT EXISTS "user" (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL DEFAULT '',
    email TEXT NOT NULL UNIQUE,
    email_verified BOOLEAN NOT NULL DEFAULT false,
    image TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Better Auth session table
CREATE TABLE IF NOT EXISTS "session" (
    id TEXT PRIMARY KEY,
    expires_at TIMESTAMPTZ NOT NULL,
    token TEXT NOT NULL UNIQUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    ip_address TEXT,
    user_agent TEXT,
    user_id TEXT NOT NULL REFERENCES "user"(id) ON DELETE CASCADE
);

-- Better Auth account table
CREATE TABLE IF NOT EXISTS "account" (
    id TEXT PRIMARY KEY,
    account_id TEXT NOT NULL,
    provider_id TEXT NOT NULL,
    user_id TEXT NOT NULL REFERENCES "user"(id) ON DELETE CASCADE,
    access_token TEXT,
    refresh_token TEXT,
    id_token TEXT,
    access_token_expires_at TIMESTAMPTZ,
    refresh_token_expires_at TIMESTAMPTZ,
    scope TEXT,
    password TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Better Auth verification table
CREATE TABLE IF NOT EXISTS "verification" (
    id TEXT PRIMARY KEY,
    identifier TEXT NOT NULL,
    value TEXT NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Change user_id columns from UUID to TEXT
ALTER TABLE trackers ALTER COLUMN user_id TYPE TEXT;
ALTER TABLE notifications ALTER COLUMN user_id TYPE TEXT;

-- Create telegram_links table
CREATE TABLE IF NOT EXISTS telegram_links (
    user_id TEXT PRIMARY KEY REFERENCES "user"(id) ON DELETE CASCADE,
    telegram_id BIGINT UNIQUE NOT NULL,
    telegram_username TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Create telegram_link_codes table for linking flow
CREATE TABLE IF NOT EXISTS telegram_link_codes (
    code TEXT PRIMARY KEY,
    user_id TEXT NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Create user_plans table
CREATE TABLE IF NOT EXISTS user_plans (
    user_id TEXT PRIMARY KEY REFERENCES "user"(id) ON DELETE CASCADE,
    plan_code TEXT NOT NULL DEFAULT 'free' REFERENCES plans(code),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Clean up old indexes, create new ones
DROP INDEX IF EXISTS idx_sessions_token_hash;
DROP INDEX IF EXISTS idx_sessions_user_id;
DROP INDEX IF EXISTS idx_login_challenges_token;
CREATE INDEX IF NOT EXISTS idx_session_token ON "session"(token);
CREATE INDEX IF NOT EXISTS idx_session_user_id ON "session"(user_id);
CREATE INDEX IF NOT EXISTS idx_trackers_user_id ON trackers(user_id);
CREATE INDEX IF NOT EXISTS idx_notifications_user_id ON notifications(user_id);
CREATE INDEX IF NOT EXISTS idx_trackers_status_next_check ON trackers(status, next_check_at);
`

const migrationV4 = `
CREATE TABLE IF NOT EXISTS telegram_states (
    telegram_id BIGINT PRIMARY KEY,
    user_id TEXT NOT NULL REFERENCES "user"(id) ON DELETE CASCADE,
    step TEXT NOT NULL,
    url TEXT,
    initial_price NUMERIC(14, 2),
    candidate_index INT NOT NULL DEFAULT 0,
    rule JSONB,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_telegram_states_user_id ON telegram_states(user_id);
`

const migrationV5 = `
ALTER TABLE telegram_states ADD COLUMN IF NOT EXISTS currency TEXT NOT NULL DEFAULT 'PLN';
`

const migrationV6 = `
ALTER TABLE trackers ADD COLUMN IF NOT EXISTS check_interval_minutes INT NOT NULL DEFAULT 180;
`

const migrationV7 = `
ALTER TABLE telegram_links ADD COLUMN IF NOT EXISTS language TEXT NOT NULL DEFAULT 'en';
UPDATE telegram_links SET language = 'en' WHERE language IS NULL OR language NOT IN ('en', 'ru', 'pl');
`

const migrationV8 = `
ALTER TABLE telegram_states ADD COLUMN IF NOT EXISTS title TEXT;
`

const migrationV9 = `
ALTER TABLE trackers ADD COLUMN IF NOT EXISTS tracking_mode TEXT NOT NULL DEFAULT 'price';
`

const migrationV10 = `
-- Align plan limits with what's advertised on the site/bot, add the Basic tier.
INSERT INTO plans (code, name, max_trackers, check_interval_minutes, price_history_days, is_paid)
VALUES
    ('free', 'Free', 3, 180, 30, false),
    ('basic', 'Basic', 10, 60, 90, true),
    ('pro', 'Pro', 50, 30, 365, true)
ON CONFLICT (code) DO UPDATE SET
    name = EXCLUDED.name,
    max_trackers = EXCLUDED.max_trackers,
    check_interval_minutes = EXCLUDED.check_interval_minutes,
    price_history_days = EXCLUDED.price_history_days,
    is_paid = EXCLUDED.is_paid;

-- Track the Tribute subscription backing a paid plan, and when it lapses.
ALTER TABLE user_plans ADD COLUMN IF NOT EXISTS tribute_subscription_id BIGINT;
ALTER TABLE user_plans ADD COLUMN IF NOT EXISTS status TEXT NOT NULL DEFAULT 'active';
ALTER TABLE user_plans ADD COLUMN IF NOT EXISTS expires_at TIMESTAMPTZ;

-- Raw log of every Tribute webhook delivery. The unique constraint on
-- (event_name, subscription_id, event_created_at) makes processing idempotent:
-- Tribute retries deliveries on failure for up to ~24h, and this rejects duplicates.
CREATE TABLE IF NOT EXISTS tribute_webhook_events (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    event_name TEXT NOT NULL,
    subscription_id BIGINT,
    event_created_at TIMESTAMPTZ,
    telegram_user_id BIGINT,
    payload JSONB NOT NULL,
    processed BOOLEAN NOT NULL DEFAULT false,
    error_message TEXT,
    received_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (event_name, subscription_id, event_created_at)
);
`

const migrationV11 = `
-- Which extraction method resolved this price point (e.g. "json_ld", "dom_attribute",
-- "microdata", "css_text", "openserp_search_result", "serper_organic_result",
-- "serper_shopping_result", "serpapi_rich_snippet"), so the admin dashboard can count
-- how often checks fall through to the paid/token-based search fallback tiers instead of
-- reading the page directly. NULL for failed checks and for older rows recorded before
-- this column existed.
ALTER TABLE price_points ADD COLUMN IF NOT EXISTS extraction_method TEXT;
`

const migrationV12 = `
-- Records a price search that failed before any tracker row existed to attach the
-- failure to — e.g. the interactive "send a link, then a price" bot flow, or /add,
-- when every extractor (direct read + search fallback) comes up empty. Without this,
-- such failures were invisible: trackers.last_error only exists once a tracker has
-- actually been created, so a search that never got that far left no trace anywhere,
-- including in the admin dashboard.
CREATE TABLE IF NOT EXISTS extraction_failures (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id TEXT NOT NULL REFERENCES "user"(id) ON DELETE CASCADE,
    url TEXT NOT NULL,
    error TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_extraction_failures_user_id ON extraction_failures(user_id);
`

const migrationV13 = `
-- Mirrors price_points.extraction_method (see V11) for stock checks: which method
-- resolved this stock_status ("keyword_scan" for the generic whole-page phrase match,
-- "json_ld" for a size-specific "zara_size" tracker, since shops.ParseZaraSizes reads the
-- same schema.org JSON-LD structured data price trackers already report as "json_ld"),
-- so the admin dashboard can show it the same way it shows price trackers' method.
ALTER TABLE stock_points ADD COLUMN IF NOT EXISTS extraction_method TEXT;
`

const migrationV14 = `
-- Pool of scraping proxies (added via internal/proxypool.Store.AddManual), re-checked for
-- aliveness hourly. "status" reflects that check, not any claim the proxy source made
-- about it. use_count/last_used_at exist so the admin dashboard can show which proxies
-- are actually carrying traffic, not just which ones exist.
CREATE TABLE IF NOT EXISTS proxies (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    address TEXT NOT NULL UNIQUE,
    protocol TEXT NOT NULL DEFAULT 'socks5',
    country_code TEXT,
    status TEXT NOT NULL DEFAULT 'unknown',
    use_count INT NOT NULL DEFAULT 0,
    last_checked_at TIMESTAMPTZ,
    last_used_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_proxies_status ON proxies(status);
`

const migrationV15 = `
-- Proxies are authenticated (username/password), since they come from a paid
-- rotating-proxy provider's IP list rather than an open/anonymous one. "source" exists in
-- case a second kind of source is ever added later.
ALTER TABLE proxies ADD COLUMN IF NOT EXISTS username TEXT;
ALTER TABLE proxies ADD COLUMN IF NOT EXISTS password TEXT;
ALTER TABLE proxies ADD COLUMN IF NOT EXISTS source TEXT NOT NULL DEFAULT 'manual';
`

const migrationV16 = `
-- Which PageFetcher tier (see extractor.FetchMethod* — "direct", "cf_relay", "render")
-- actually produced the page body, tracked separately from extraction_method: the same
-- extraction_method (e.g. "json_ld") can come from a page fetched three different ways,
-- and without this there's no way to tell whether the Cloudflare relay tier is actually
-- being exercised. NULL for checks where no PageFetcher tier applies (css_text, search
-- fallback rule types) or for rows recorded before this column existed.
ALTER TABLE price_points ADD COLUMN IF NOT EXISTS fetch_method TEXT;
ALTER TABLE stock_points ADD COLUMN IF NOT EXISTS fetch_method TEXT;
`

const migrationV17 = `
ALTER TABLE user_plans ADD COLUMN IF NOT EXISTS trial_used BOOLEAN NOT NULL DEFAULT false;

INSERT INTO plans (code, name, max_trackers, check_interval_minutes, price_history_days, is_paid)
VALUES ('trial', 'Trial', 10, 60, 90, false)
ON CONFLICT (code) DO UPDATE SET
    name = EXCLUDED.name,
    max_trackers = EXCLUDED.max_trackers,
    check_interval_minutes = EXCLUDED.check_interval_minutes,
    price_history_days = EXCLUDED.price_history_days,
    is_paid = EXCLUDED.is_paid;
`

func RunMigrations(ctx context.Context, pool *pgxpool.Pool, logger zerolog.Logger) error {
	for _, m := range Migrations {
		logger.Info().Int("version", m.Version).Str("desc", m.Description).Msg("running migration")
		_, err := pool.Exec(ctx, m.SQL)
		if err != nil {
			return fmt.Errorf("migration v%d: %w", m.Version, err)
		}
	}

	// create a migrations tracking table and mark applied
	_, err := pool.Exec(ctx, `CREATE TABLE IF NOT EXISTS _migrations (version INT PRIMARY KEY, applied_at TIMESTAMPTZ NOT NULL DEFAULT now())`)
	if err != nil {
		return fmt.Errorf("create migrations table: %w", err)
	}

	for _, m := range Migrations {
		_, err := pool.Exec(ctx, `INSERT INTO _migrations (version) VALUES ($1) ON CONFLICT DO NOTHING`, m.Version)
		if err != nil {
			return fmt.Errorf("record migration v%d: %w", m.Version, err)
		}
		time.Sleep(10 * time.Millisecond) // small delay for ordering
	}

	logger.Info().Int("count", len(Migrations)).Msg("migrations complete")
	return nil
}
