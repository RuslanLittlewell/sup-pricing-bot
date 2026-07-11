package handler

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/littlewell/price-tracker/internal/extractor"
	"github.com/littlewell/price-tracker/internal/proxypool"
	"github.com/littlewell/price-tracker/internal/renderer"
	"github.com/littlewell/price-tracker/internal/security"
)

type playgroundResponse struct {
	Tool        string                      `json:"tool"`
	URL         string                      `json:"url"`
	DurationMS  int64                       `json:"durationMs"`
	FetchMethod string                      `json:"fetchMethod,omitempty"`
	BodyBytes   int                         `json:"bodyBytes,omitempty"`
	StockStatus string                      `json:"stockStatus,omitempty"`
	Result      *extractor.ExtractionResult `json:"result,omitempty"`
	Error       string                      `json:"error,omitempty"`
}

// AdminPlayground runs one explicitly selected extraction tier without creating or
// changing a tracker. It is intentionally admin-only and returns structured diagnostics
// rather than raw page HTML, which can be very large and may contain user-specific data.
func AdminPlayground(pool *pgxpool.Pool, rend *renderer.Renderer, fetcher *extractor.PageFetcher) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			URL  string `json:"url"`
			Tool string `json:"tool"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.URL == "" || req.Tool == "" {
			http.Error(w, `{"error":"url and tool are required"}`, http.StatusBadRequest)
			return
		}
		if err := security.ValidateURL(req.URL); err != nil {
			http.Error(w, `{"error":"invalid or unsafe URL"}`, http.StatusBadRequest)
			return
		}

		started := time.Now()
		out := playgroundResponse{Tool: req.Tool, URL: req.URL}
		var err error

		switch req.Tool {
		case "fetch_chain":
			var body []byte
			body, out.FetchMethod, err = fetcher.Fetch(req.URL)
			out.BodyBytes = len(body)
			if err == nil {
				out.StockStatus = extractor.DetectStockStatusFromText(body)
			}
		case "renderer":
			var body string
			body, err = rend.Render(r.Context(), req.URL, 4*time.Second)
			out.FetchMethod = extractor.FetchMethodRender
			out.BodyBytes = len(body)
		case "attribute", "generic":
			var body []byte
			body, out.FetchMethod, err = fetcher.Fetch(req.URL)
			out.BodyBytes = len(body)
			if err == nil {
				if req.Tool == "attribute" {
					out.Result, err = extractor.NewAttribute().Extract(body, req.URL)
				} else {
					out.Result, err = extractor.NewGeneric().Extract(body, req.URL)
				}
			}
		case "stock":
			var body []byte
			body, out.FetchMethod, err = fetcher.Fetch(req.URL)
			out.BodyBytes = len(body)
			if err == nil {
				out.StockStatus = extractor.DetectStockStatusFromText(body)
			}
		case "search_fallback", "searxng", "openserp", "serper", "serpapi", "gemini":
			var selected extractor.Extractor
			switch req.Tool {
			case "search_fallback":
				selected = extractor.NewSearchFallback(proxypool.NewStore(pool))
			case "searxng":
				selected = extractor.NewSearXNG()
			case "openserp":
				selected = extractor.NewOpenSERP(proxypool.NewStore(pool))
			case "serper":
				selected = extractor.NewSerper()
			case "serpapi":
				selected = extractor.NewSerpAPI()
			case "gemini":
				selected = extractor.NewGemini()
			}
			if selected == nil {
				err = &playgroundError{"tool is not configured"}
			} else {
				out.Result, err = selected.Extract(nil, req.URL)
			}
		default:
			http.Error(w, `{"error":"unknown tool"}`, http.StatusBadRequest)
			return
		}

		out.DurationMS = time.Since(started).Milliseconds()
		if err != nil {
			out.Error = err.Error()
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(out)
	}
}

type playgroundError struct{ message string }

func (e *playgroundError) Error() string { return e.message }
