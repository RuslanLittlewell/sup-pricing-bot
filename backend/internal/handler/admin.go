package handler

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"
)

// adminSearchExtractionMethods are the rule types set by the token/API-key-based search
// fallback tiers (OpenSERP, Serper, SerpAPI) — see extractor.RuleType and each
// extractor's own "type" value. Everything else (json_ld, dom_attribute, meta_tag,
// microdata, css_selector, css_text) came from reading the page directly, for free.
var adminSearchExtractionMethods = []string{
	"openserp_search_result",
	"openserp_extract",
	"serper_organic_result",
	"serper_shopping_result",
	"serpapi_rich_snippet",
}

type adminUser struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Email        string `json:"email"`
	PlanCode     string `json:"planCode"`
	TrackerCount int    `json:"trackerCount"`
}

type adminUserTracker struct {
	ID                     string   `json:"id"`
	Title                  string   `json:"title"`
	URL                    string   `json:"url"`
	Domain                 string   `json:"domain"`
	Status                 string   `json:"status"`
	InitialPrice           float64  `json:"initialPrice"`
	CurrentPrice           *float64 `json:"currentPrice"`
	Currency               string   `json:"currency"`
	ExtractionMethod       string   `json:"extractionMethod"`
	LatestExtractionMethod string   `json:"latestExtractionMethod,omitempty"`
	CreatedAt              string   `json:"createdAt"`
	LastCheckedAt          *string  `json:"lastCheckedAt"`
	ConsecutiveErrors      int      `json:"consecutiveErrors"`
	LastError              *string  `json:"lastError"`
}

type adminFallbackTracker struct {
	UserID    string `json:"userId"`
	UserName  string `json:"userName"`
	Title     string `json:"title"`
	URL       string `json:"url"`
	Method    string `json:"method"`
	Timestamp string `json:"timestamp"`
}

type adminFailedTracker struct {
	ID        string `json:"id"`
	Kind      string `json:"kind"`
	UserID    string `json:"userId"`
	UserName  string `json:"userName"`
	Title     string `json:"title"`
	URL       string `json:"url"`
	Error     string `json:"error"`
	Timestamp string `json:"timestamp"`
}

type adminTrackersResponse struct {
	FallbackTrackers []adminFallbackTracker `json:"fallbackTrackers"`
	FailedTrackers   []adminFailedTracker   `json:"failedTrackers"`
	GeneratedAt      string                 `json:"generatedAt"`
}

// AdminAuth requires HTTP Basic Auth matching the configured admin username/password, so
// the admin dashboard can be called remotely (over the public API domain) without an SSH
// tunnel to the database. Fails closed: if either credential is unconfigured, every
// request is rejected rather than falling back to some default.
func AdminAuth(username, password string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotUser, gotPass, ok := r.BasicAuth()
			validUser := username != "" && subtle.ConstantTimeCompare([]byte(gotUser), []byte(username)) == 1
			validPass := password != "" && subtle.ConstantTimeCompare([]byte(gotPass), []byte(password)) == 1
			if !ok || !validUser || !validPass {
				w.Header().Set("WWW-Authenticate", `Basic realm="admin"`)
				http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// AdminCors allows the admin dashboard (run locally, from any localhost/127.0.0.1 port —
// Vite's dev server hops ports when one is busy) to call these routes cross-origin from
// the deployed API domain. Kept separate from the main app's Cors middleware, which is
// scoped to the production frontend's single origin.
func AdminCors() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")
			if isLocalOrigin(origin) {
				w.Header().Set("Access-Control-Allow-Origin", origin)
				w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
				w.Header().Set("Access-Control-Allow-Methods", "GET, DELETE, OPTIONS")
			}
			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func isLocalOrigin(origin string) bool {
	return strings.HasPrefix(origin, "http://localhost:") ||
		origin == "http://localhost" ||
		strings.HasPrefix(origin, "http://127.0.0.1:") ||
		origin == "http://127.0.0.1"
}

// AdminUsers serves the users page: id, display name, current subscription plan, and how
// many trackers each user has.
func AdminUsers(pool *pgxpool.Pool, log zerolog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
		defer cancel()

		rows, err := pool.Query(ctx, `
			SELECT u.id, u.name, u.email, COALESCE(up.plan_code, 'free'),
			       COUNT(t.id) FILTER (WHERE t.status != 'deleted')
			FROM "user" u
			LEFT JOIN user_plans up ON up.user_id = u.id
			LEFT JOIN trackers t ON t.user_id = u.id
			GROUP BY u.id, u.name, u.email, up.plan_code
			ORDER BY u.created_at DESC
		`)
		if err != nil {
			log.Error().Err(err).Msg("failed to query admin users")
			http.Error(w, `{"error":"failed to load data"}`, http.StatusInternalServerError)
			return
		}
		defer rows.Close()

		users := []adminUser{}
		for rows.Next() {
			var u adminUser
			if err := rows.Scan(&u.ID, &u.Name, &u.Email, &u.PlanCode, &u.TrackerCount); err != nil {
				log.Error().Err(err).Msg("failed to scan admin user row")
				http.Error(w, `{"error":"failed to load data"}`, http.StatusInternalServerError)
				return
			}
			users = append(users, u)
		}

		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(users); err != nil {
			log.Error().Err(err).Msg("failed to encode admin users response")
		}
	}
}

// AdminUserTrackers serves a single user's trackers: what they're tracking, and — the
// point of this view — how each one's price gets resolved (extraction_rule's "type",
// set once at creation time; see extractor.RuleType and each extractor's own rule
// literal), so a support question like "why does this tracker behave oddly" can be
// answered by seeing whether it reads the page directly (json_ld, dom_attribute,
// meta_tag, microdata, css_selector, css_text) or fell back to a paid search API.
func AdminUserTrackers(pool *pgxpool.Pool, log zerolog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID := chi.URLParam(r, "id")
		if userID == "" {
			http.Error(w, `{"error":"missing user id"}`, http.StatusBadRequest)
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
		defer cancel()

		rows, err := pool.Query(ctx, `
			SELECT t.id, COALESCE(NULLIF(t.title, ''), t.domain), t.url, t.domain, t.status,
			       t.initial_price, t.current_price, t.currency,
			       COALESCE(t.extraction_rule->>'type', 'unknown'),
			       COALESCE((
			           SELECT pp.extraction_method FROM price_points pp
			           WHERE pp.tracker_id = t.id
			           ORDER BY pp.checked_at DESC LIMIT 1
			       ), ''),
			       t.created_at::text, t.last_checked_at::text, t.consecutive_errors, t.last_error
			FROM trackers t
			WHERE t.user_id = $1 AND t.status != 'deleted'
			ORDER BY t.created_at DESC
		`, userID)
		if err != nil {
			log.Error().Err(err).Str("user_id", userID).Msg("failed to query admin user trackers")
			http.Error(w, `{"error":"failed to load data"}`, http.StatusInternalServerError)
			return
		}
		defer rows.Close()

		trackers := []adminUserTracker{}
		for rows.Next() {
			var (
				t             adminUserTracker
				lastCheckedAt *string
				latestMethod  string
			)
			if err := rows.Scan(&t.ID, &t.Title, &t.URL, &t.Domain, &t.Status,
				&t.InitialPrice, &t.CurrentPrice, &t.Currency,
				&t.ExtractionMethod, &latestMethod,
				&t.CreatedAt, &lastCheckedAt, &t.ConsecutiveErrors, &t.LastError); err != nil {
				log.Error().Err(err).Msg("failed to scan admin user tracker row")
				http.Error(w, `{"error":"failed to load data"}`, http.StatusInternalServerError)
				return
			}
			t.LastCheckedAt = lastCheckedAt
			// Only surface this when it differs from how the tracker was originally set up —
			// e.g. it started reading the page directly but has since had to fall back to a
			// search API. Keeps the common, unremarkable case (always the same method) from
			// cluttering every row with a redundant second badge.
			if latestMethod != "" && latestMethod != t.ExtractionMethod {
				t.LatestExtractionMethod = latestMethod
			}
			trackers = append(trackers, t)
		}

		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(trackers); err != nil {
			log.Error().Err(err).Msg("failed to encode admin user trackers response")
		}
	}
}

// AdminTrackers serves the trackers page: which tracked links resolved through a paid
// search fallback (and which one), and which are currently failing to extract at all —
// each row naming the owning user, so a failure or fallback usage can be traced back to
// who added the link.
func AdminTrackers(pool *pgxpool.Pool, log zerolog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
		defer cancel()

		data, err := loadAdminTrackers(ctx, pool)
		if err != nil {
			log.Error().Err(err).Msg("failed to load admin trackers")
			http.Error(w, `{"error":"failed to load data"}`, http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(data); err != nil {
			log.Error().Err(err).Msg("failed to encode admin trackers response")
		}
	}
}

// AdminDeleteFailedTracker removes an item from the admin "failed extraction" list.
// Items backed by extraction_failures are deleted; items backed by a live tracker have
// last_error cleared so the tracker stays active but no longer appears in the admin list.
func AdminDeleteFailedTracker(pool *pgxpool.Pool, log zerolog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		kind := chi.URLParam(r, "kind")
		id := chi.URLParam(r, "id")
		if id == "" {
			http.Error(w, `{"error":"missing id"}`, http.StatusBadRequest)
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()

		var (
			tag pgconn.CommandTag
			err error
		)
		switch kind {
		case "extraction_failure":
			tag, err = pool.Exec(ctx, `
				DELETE FROM extraction_failures ef
				USING (
					SELECT user_id, url
					FROM extraction_failures
					WHERE id = $1
				) target
				WHERE ef.user_id = target.user_id AND ef.url = target.url
			`, id)
		case "tracker_error":
			tag, err = pool.Exec(ctx, `
				UPDATE trackers
				SET last_error = NULL, consecutive_errors = 0, updated_at = now()
				WHERE id = $1
			`, id)
		default:
			http.Error(w, `{"error":"unknown failed item kind"}`, http.StatusBadRequest)
			return
		}
		if err != nil {
			log.Error().Err(err).Str("kind", kind).Str("id", id).Msg("failed to delete admin failed tracker item")
			http.Error(w, `{"error":"failed to delete item"}`, http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true,"deleted":` + strconv.FormatInt(tag.RowsAffected(), 10) + `}`))
	}
}

func loadAdminTrackers(ctx context.Context, pool *pgxpool.Pool) (*adminTrackersResponse, error) {
	resp := &adminTrackersResponse{
		FallbackTrackers: []adminFallbackTracker{},
		FailedTrackers:   []adminFailedTracker{},
		GeneratedAt:      time.Now().Format(time.RFC1123),
	}

	fallbackRows, err := pool.Query(ctx, `
		SELECT DISTINCT ON (t.id)
			u.id, COALESCE(NULLIF(u.name, ''), u.email),
			COALESCE(t.title, t.domain), t.url, pp.extraction_method, pp.checked_at::text
		FROM price_points pp
		JOIN trackers t ON t.id = pp.tracker_id
		JOIN "user" u ON u.id = t.user_id
		WHERE pp.status = 'success' AND pp.extraction_method = ANY($1) AND t.status != 'deleted'
		ORDER BY t.id, pp.checked_at DESC
	`, adminSearchExtractionMethods)
	if err != nil {
		return nil, err
	}
	defer fallbackRows.Close()
	for fallbackRows.Next() {
		var f adminFallbackTracker
		if err := fallbackRows.Scan(&f.UserID, &f.UserName, &f.Title, &f.URL, &f.Method, &f.Timestamp); err != nil {
			return nil, err
		}
		resp.FallbackTrackers = append(resp.FallbackTrackers, f)
	}

	// Two sources of failure, combined: trackers that exist and have a recorded
	// last_error (from a worker check or a manual /check), and extraction_failures —
	// searches that failed before any tracker was ever created (e.g. /add, or the
	// interactive "send a link, then a price" flow), which otherwise leave no trace at
	// all since there's no tracker row to hang last_error off of.
	failedRows, err := pool.Query(ctx, `
		(
			SELECT t.id::text, 'tracker_error', u.id, COALESCE(NULLIF(u.name, ''), u.email),
			       COALESCE(t.title, t.domain), t.url, t.last_error, t.last_checked_at::text
			FROM trackers t
			JOIN "user" u ON u.id = t.user_id
			WHERE t.status != 'deleted' AND t.last_error IS NOT NULL AND t.last_error != ''
			  AND NOT EXISTS (
			      SELECT 1
			      FROM price_points pp
			      WHERE pp.tracker_id = t.id
			        AND pp.status = 'success'
			        AND pp.extraction_method = ANY($1)
			  )
		)
		UNION ALL
		(
			SELECT id, kind, user_id, user_name, title, url, error, timestamp
			FROM (
				SELECT DISTINCT ON (ef.user_id, ef.url)
				       ef.id::text AS id,
				       'extraction_failure' AS kind,
				       u.id AS user_id,
				       COALESCE(NULLIF(u.name, ''), u.email) AS user_name,
				       ef.url AS title,
				       ef.url AS url,
				       ef.error AS error,
				       ef.created_at::text AS timestamp
				FROM extraction_failures ef
				JOIN "user" u ON u.id = ef.user_id
				WHERE NOT EXISTS (
				      SELECT 1
				      FROM trackers t
				      JOIN price_points pp ON pp.tracker_id = t.id
				      WHERE t.status != 'deleted'
				        AND t.url = ef.url
				        AND pp.status = 'success'
				        AND pp.extraction_method = ANY($1)
				)
				ORDER BY ef.user_id, ef.url, ef.created_at DESC
			) latest_failures
		)
		ORDER BY 8 DESC NULLS LAST
		LIMIT 100
	`, adminSearchExtractionMethods)
	if err != nil {
		return nil, err
	}
	defer failedRows.Close()
	for failedRows.Next() {
		var f adminFailedTracker
		if err := failedRows.Scan(&f.ID, &f.Kind, &f.UserID, &f.UserName, &f.Title, &f.URL, &f.Error, &f.Timestamp); err != nil {
			return nil, err
		}
		resp.FailedTrackers = append(resp.FailedTrackers, f)
	}

	return resp, nil
}
