package handler

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"

	"github.com/littlewell/price-tracker/internal/config"
)

const maxTributeBodySize = 1 << 20 // 1MB

var (
	errUnknownSubscription  = errors.New("unknown tribute subscription_id, no plan mapping configured")
	errUnlinkedTelegramUser = errors.New("no user linked to this telegram_user_id")
)

type tributeWebhookEnvelope struct {
	Name      string          `json:"name"`
	CreatedAt time.Time       `json:"created_at"`
	Payload   json.RawMessage `json:"payload"`
}

type tributeSubscriptionPayload struct {
	SubscriptionID   int64      `json:"subscription_id"`
	SubscriptionName string     `json:"subscription_name"`
	Type             string     `json:"type"`
	TelegramUserID   int64      `json:"telegram_user_id"`
	TelegramUsername string     `json:"telegram_username"`
	ExpiresAt        *time.Time `json:"expires_at"`
}

// TributeWebhook handles subscription lifecycle events from Tribute
// (https://wiki.tribute.tg/for-content-creators/api-documentation/webhooks).
//
// Event payload field names below were confirmed from a real "new_subscription"
// delivery. The "cancelled_subscription" / "renewed_subscription" names follow the
// same snake_case convention by inference — not yet confirmed against a live
// delivery, so double check the logs (tribute_webhook_events) once those fire.
func TributeWebhook(pool *pgxpool.Pool, cfg *config.Config, baseLog zerolog.Logger) http.HandlerFunc {
	planBySubscriptionID := map[int64]string{
		cfg.TributeBasicSubscriptionID: "basic",
		cfg.TributeProSubscriptionID:   "pro",
	}

	return func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(io.LimitReader(r.Body, maxTributeBodySize))
		if err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}

		if !verifyTributeSignature(body, r.Header.Get("trbt-signature"), cfg.TributeAPIKey) {
			baseLog.Warn().Msg("tribute webhook: invalid or missing signature")
			http.Error(w, "invalid signature", http.StatusUnauthorized)
			return
		}

		var event tributeWebhookEnvelope
		if err := json.Unmarshal(body, &event); err != nil {
			baseLog.Error().Err(err).Msg("tribute webhook: failed to parse envelope")
			http.Error(w, "invalid body", http.StatusBadRequest)
			return
		}

		var sub tributeSubscriptionPayload
		if len(event.Payload) > 0 {
			if err := json.Unmarshal(event.Payload, &sub); err != nil {
				baseLog.Error().Err(err).Str("event", event.Name).Msg("tribute webhook: failed to parse subscription payload")
				http.Error(w, "invalid payload", http.StatusBadRequest)
				return
			}
		}

		ctx := r.Context()
		// Per-request logger: must be a new local value, not a reassignment of the
		// closure-captured baseLog — the handler runs concurrently across requests,
		// so mutating a shared variable here would be a data race and leak fields
		// (e.g. subscription_id) from one request's logs into another's.
		log := baseLog.With().Str("event", event.Name).Int64("subscription_id", sub.SubscriptionID).
			Int64("telegram_user_id", sub.TelegramUserID).Logger()

		// Idempotency: Tribute retries failed deliveries for ~24h, so the same event
		// can arrive more than once. The unique constraint rejects re-processing.
		var inserted bool
		err = pool.QueryRow(ctx, `
			INSERT INTO tribute_webhook_events (event_name, subscription_id, event_created_at, telegram_user_id, payload)
			VALUES ($1, $2, $3, $4, $5)
			ON CONFLICT (event_name, subscription_id, event_created_at) DO NOTHING
			RETURNING true
		`, event.Name, sub.SubscriptionID, event.CreatedAt, sub.TelegramUserID, body).Scan(&inserted)
		if err != nil && err != pgx.ErrNoRows {
			log.Error().Err(err).Msg("tribute webhook: failed to record event")
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		if !inserted {
			log.Info().Msg("tribute webhook: duplicate delivery, already processed")
			w.WriteHeader(http.StatusOK)
			return
		}

		if applyErr := applyTributeEvent(ctx, pool, event.Name, sub, planBySubscriptionID, log); applyErr != nil {
			markTributeEventError(ctx, pool, event.Name, sub.SubscriptionID, event.CreatedAt, applyErr.Error())
			log.Warn().Err(applyErr).Msg("tribute webhook: event stored but not applied")
			// Acknowledge anyway — this is a data/mapping problem on our side (unknown
			// plan or unlinked user), not a delivery problem. Retrying won't fix it.
			w.WriteHeader(http.StatusOK)
			return
		}

		_, _ = pool.Exec(ctx, `
			UPDATE tribute_webhook_events SET processed = true
			WHERE event_name = $1 AND subscription_id = $2 AND event_created_at = $3
		`, event.Name, sub.SubscriptionID, event.CreatedAt)

		w.WriteHeader(http.StatusOK)
	}
}

func applyTributeEvent(ctx context.Context, pool *pgxpool.Pool, eventName string, sub tributeSubscriptionPayload, planBySubscriptionID map[int64]string, log zerolog.Logger) error {
	switch eventName {
	case "new_subscription", "renewed_subscription":
		planCode, ok := planBySubscriptionID[sub.SubscriptionID]
		if !ok {
			return errUnknownSubscription
		}

		userID, err := userIDByTelegramID(ctx, pool, sub.TelegramUserID)
		if err != nil {
			return err
		}

		_, err = pool.Exec(ctx, `
			INSERT INTO user_plans (user_id, plan_code, tribute_subscription_id, status, expires_at, updated_at)
			VALUES ($1, $2, $3, 'active', $4, now())
			ON CONFLICT (user_id) DO UPDATE SET
				plan_code = $2, tribute_subscription_id = $3, status = 'active', expires_at = $4, updated_at = now()
		`, userID, planCode, sub.SubscriptionID, sub.ExpiresAt)
		return err

	case "cancelled_subscription":
		userID, err := userIDByTelegramID(ctx, pool, sub.TelegramUserID)
		if err != nil {
			return err
		}

		_, err = pool.Exec(ctx, `
			UPDATE user_plans SET status = 'cancelled', updated_at = now()
			WHERE user_id = $1 AND tribute_subscription_id = $2
		`, userID, sub.SubscriptionID)
		return err

	default:
		log.Info().Msg("tribute webhook: unhandled event type, ignoring")
		return nil
	}
}

func markTributeEventError(ctx context.Context, pool *pgxpool.Pool, eventName string, subscriptionID int64, createdAt time.Time, message string) {
	pool.Exec(ctx, `
		UPDATE tribute_webhook_events SET error_message = $4
		WHERE event_name = $1 AND subscription_id = $2 AND event_created_at = $3
	`, eventName, subscriptionID, createdAt, message)
}

func userIDByTelegramID(ctx context.Context, pool *pgxpool.Pool, telegramID int64) (string, error) {
	var userID string
	err := pool.QueryRow(ctx, `SELECT user_id FROM telegram_links WHERE telegram_id = $1`, telegramID).Scan(&userID)
	if err != nil {
		return "", errUnlinkedTelegramUser
	}
	return userID, nil
}

func verifyTributeSignature(body []byte, signatureHeader, apiKey string) bool {
	if signatureHeader == "" || apiKey == "" {
		return false
	}

	mac := hmac.New(sha256.New, []byte(apiKey))
	mac.Write(body)
	expected := mac.Sum(nil)

	got, err := hex.DecodeString(signatureHeader)
	if err != nil {
		return false
	}

	return hmac.Equal(expected, got)
}
