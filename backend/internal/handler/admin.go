package handler

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"
)

// adminSearchExtractionMethods are the rule types set by the token/API-key-based search
// fallback tiers (OpenSERP, Serper, SerpAPI) — see extractor.RuleType and each
// extractor's own "type" value. Everything else (json_ld, dom_attribute, meta_tag,
// microdata, css_selector, css_text) came from reading the page directly, for free.
var adminSearchExtractionMethods = []string{
	"openserp_search_result",
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

type adminFallbackTracker struct {
	UserID    string `json:"userId"`
	UserName  string `json:"userName"`
	Title     string `json:"title"`
	URL       string `json:"url"`
	Method    string `json:"method"`
	Timestamp string `json:"timestamp"`
}

type adminFailedTracker struct {
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
			SELECT u.id, COALESCE(NULLIF(u.name, ''), u.email),
			       COALESCE(t.title, t.domain), t.url, t.last_error, t.last_checked_at::text
			FROM trackers t
			JOIN "user" u ON u.id = t.user_id
			WHERE t.status != 'deleted' AND t.last_error IS NOT NULL AND t.last_error != ''
		)
		UNION ALL
		(
			SELECT u.id, COALESCE(NULLIF(u.name, ''), u.email),
			       ef.url, ef.url, ef.error, ef.created_at::text
			FROM extraction_failures ef
			JOIN "user" u ON u.id = ef.user_id
		)
		ORDER BY 6 DESC NULLS LAST
		LIMIT 100
	`)
	if err != nil {
		return nil, err
	}
	defer failedRows.Close()
	for failedRows.Next() {
		var f adminFailedTracker
		if err := failedRows.Scan(&f.UserID, &f.UserName, &f.Title, &f.URL, &f.Error, &f.Timestamp); err != nil {
			return nil, err
		}
		resp.FailedTrackers = append(resp.FailedTrackers, f)
	}

	return resp, nil
}
