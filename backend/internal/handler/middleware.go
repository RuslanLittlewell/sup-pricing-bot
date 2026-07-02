package handler

import (
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5/middleware"
	"github.com/rs/zerolog"
)

func Logger(logger zerolog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
			t1 := time.Now()
			defer func() {
				logger.Info().
					Str("method", r.Method).
					Str("path", r.URL.Path).
					Int("status", ww.Status()).
					Int("bytes", ww.BytesWritten()).
					Dur("duration", time.Since(t1)).
					Msg("request")
			}()
			next.ServeHTTP(ww, r)
		})
	}
}

func Cors(origin string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// /api/admin/* has its own CORS policy (AdminCors, in admin.go) —
			// reflecting any localhost/127.0.0.1 origin for the locally-run admin
			// dashboard — instead of this single fixed production frontend origin.
			// Skip entirely here so this middleware's OPTIONS short-circuit below
			// doesn't answer admin preflight requests with the wrong origin.
			if strings.HasPrefix(r.URL.Path, "/api/admin/") {
				next.ServeHTTP(w, r)
				return
			}

			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Session-Token")
			w.Header().Set("Access-Control-Allow-Credentials", "true")

			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

func RateLimit() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r)
		})
	}
}

func Timeout(timeout time.Duration) func(http.Handler) http.Handler {
	return middleware.Timeout(timeout)
}
