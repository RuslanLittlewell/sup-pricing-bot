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
