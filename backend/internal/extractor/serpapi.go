package extractor

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	serpAPIBaseURL = "https://serpapi.com/search"
	// serpAPITimeout is generous because SerpApi only returns fast (sub-second) when the
	// query is already cached on their end; a cold/uncached Google search routinely takes
	// 15-20+ seconds to complete server-side before SerpApi responds at all.
	serpAPITimeout = 45 * time.Second
)

// SerpAPIExtractor is the pipeline's only tier that finds a product's price without
// fetching the product page at all: it asks SerpApi (serpapi.com) to search Google for
// the exact product URL, and reads the price off the "rich snippet" Google already
// extracted from that page's schema.org Product markup during its own crawl.
//
// This works even when the page itself blocks bots (DataDome/Akamai/Cloudflare, etc.),
// because it never requests the target page directly — it relies on data Google's own
// crawler (which most sites allow) already collected and cached. The trade-off is that
// the price reflects Google's last crawl of the page, not necessarily the live price,
// and it only exists at all for pages whose structured data Google chose to surface as a
// rich snippet.
type SerpAPIExtractor struct {
	apiKeys []string
	// nextKeyIndex is the key to try first on the *next* call. It only advances when a
	// key is rejected (quota exhausted or invalid) — sticking with whichever key last
	// worked avoids re-probing an exhausted key on every subsequent call from a
	// long-lived extractor instance (e.g. the worker's, which is constructed once and
	// reused across every tracker check).
	nextKeyIndex int
	mu           sync.Mutex
	httpClient   *http.Client
}

// NewSerpAPI returns nil (tier disabled) if no SerpApi key is configured, so this tier
// can be wired in unconditionally instead of needing a separate feature flag. It reads
// SERPAPI_KEY as the primary key, plus any SERPAPI_KEY_2, SERPAPI_KEY_3, ... additional
// keys (stopping at the first missing suffix) to rotate through when one account runs
// out of searches for the billing period.
func NewSerpAPI() *SerpAPIExtractor {
	keys := collectSerpAPIKeys()
	if len(keys) == 0 {
		return nil
	}
	return &SerpAPIExtractor{
		apiKeys:    keys,
		httpClient: &http.Client{Timeout: serpAPITimeout},
	}
}

func collectSerpAPIKeys() []string {
	var keys []string
	if k := os.Getenv("SERPAPI_KEY"); k != "" {
		keys = append(keys, k)
	}
	for i := 2; ; i++ {
		k := os.Getenv(fmt.Sprintf("SERPAPI_KEY_%d", i))
		if k == "" {
			break
		}
		keys = append(keys, k)
	}
	return keys
}

func (e *SerpAPIExtractor) Domain() string { return "*serpapi" }

// Extract ignores the html parameter: it never sees the target page's own markup, only
// Google's cached rich snippet for it. Callers may pass nil when the page itself
// couldn't be fetched at all — that is, in fact, this tier's main reason to exist.
//
// It rotates through the configured keys on a 429 (quota exhausted / hourly limit hit)
// or 401 (key invalid/revoked) response — per SerpApi's documented status codes
// (https://serpapi.com/api-status-and-error-codes) — trying each configured key at most
// once before giving up.
func (e *SerpAPIExtractor) Extract(_ []byte, pageURL string) (*ExtractionResult, error) {
	empty := &ExtractionResult{Candidates: []PriceCandidate{}}
	if e == nil {
		return empty, nil
	}

	e.mu.Lock()
	startIndex := e.nextKeyIndex
	e.mu.Unlock()

	var lastErr error
	for attempt := 0; attempt < len(e.apiKeys); attempt++ {
		keyIndex := (startIndex + attempt) % len(e.apiKeys)

		body, status, err := e.doRequest(pageURL, e.apiKeys[keyIndex])
		if err != nil {
			lastErr = err
			continue
		}

		if status == http.StatusTooManyRequests || status == http.StatusUnauthorized {
			lastErr = fmt.Errorf("serpapi key #%d rejected with status %d", keyIndex+1, status)
			e.mu.Lock()
			e.nextKeyIndex = (keyIndex + 1) % len(e.apiKeys)
			e.mu.Unlock()
			continue
		}
		if status != http.StatusOK {
			return nil, fmt.Errorf("serpapi returned status %d", status)
		}

		return parseSerpAPIResponse(body, pageURL)
	}

	return nil, fmt.Errorf("all %d configured serpapi key(s) exhausted: %w", len(e.apiKeys), lastErr)
}

// doRequest performs the actual HTTP call for one key and returns the raw response body
// alongside the status code, leaving interpretation of that status (rotate vs. hard
// fail) to the caller.
func (e *SerpAPIExtractor) doRequest(pageURL, apiKey string) ([]byte, int, error) {
	endpoint := fmt.Sprintf("%s?engine=google&gl=pl&hl=pl&num=5&q=%s&api_key=%s",
		serpAPIBaseURL, url.QueryEscape(pageURL), url.QueryEscape(apiKey))

	ctx, cancel := context.WithTimeout(context.Background(), serpAPITimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "GET", endpoint, nil)
	if err != nil {
		return nil, 0, fmt.Errorf("build serpapi request: %w", err)
	}

	resp, err := e.httpClient.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("serpapi request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, 0, fmt.Errorf("read serpapi response: %w", err)
	}
	return body, resp.StatusCode, nil
}

func parseSerpAPIResponse(body []byte, pageURL string) (*ExtractionResult, error) {
	empty := &ExtractionResult{Candidates: []PriceCandidate{}}

	var parsed serpAPIResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("parse serpapi response: %w", err)
	}

	match := bestMatchingResult(parsed.OrganicResults, pageURL)
	if match == nil {
		return empty, nil
	}

	ext := match.RichSnippet.Top.DetectedExtensions
	if ext == nil {
		ext = match.RichSnippet.Bottom.DetectedExtensions
	}
	if ext == nil || ext.Price == "" {
		return empty, nil
	}

	rule, _ := json.Marshal(map[string]string{"type": "serpapi_rich_snippet"})
	return &ExtractionResult{
		Title: match.Title,
		Candidates: []PriceCandidate{{
			Price:      string(ext.Price),
			Currency:   normalizeCurrencySymbol(ext.Currency),
			Confidence: 0.7,
			Label:      "SerpApi rich snippet",
			SourceURL:  match.Link,
			Rule:       rule,
		}},
	}, nil
}

// bestMatchingResult only accepts the organic result whose link matches the URL we
// searched for. Search APIs can return nearby products too, so this keeps the fallback
// tied to the exact page the user asked us to track.
func bestMatchingResult(results []serpAPIOrganicResult, pageURL string) *serpAPIOrganicResult {
	for i := range results {
		if SameURL(results[i].Link, pageURL) {
			return &results[i]
		}
	}
	return nil
}

func normalizeCurrencySymbol(symbol string) string {
	switch strings.TrimSpace(symbol) {
	case "zł", "PLN":
		return "PLN"
	case "$", "USD":
		return "USD"
	case "€", "EUR":
		return "EUR"
	case "£", "GBP":
		return "GBP"
	default:
		return symbol
	}
}

type serpAPIResponse struct {
	OrganicResults []serpAPIOrganicResult `json:"organic_results"`
}

type serpAPIOrganicResult struct {
	Title       string `json:"title"`
	Link        string `json:"link"`
	RichSnippet struct {
		Top    serpAPIRichSnippetSection `json:"top"`
		Bottom serpAPIRichSnippetSection `json:"bottom"`
	} `json:"rich_snippet"`
}

type serpAPIRichSnippetSection struct {
	DetectedExtensions *serpAPIDetectedExtensions `json:"detected_extensions"`
}

type serpAPIDetectedExtensions struct {
	Price    flexiblePriceString `json:"price"`
	Currency string              `json:"currency"`
}

// flexiblePriceString unmarshals a JSON field that SerpApi returns as a number for some
// results (e.g. 439) and as a string for others (e.g. "439.00"), normalizing either into
// a plain string so it feeds into the same price-parsing path as every other extractor.
type flexiblePriceString string

func (f *flexiblePriceString) UnmarshalJSON(data []byte) error {
	var asString string
	if err := json.Unmarshal(data, &asString); err == nil {
		*f = flexiblePriceString(asString)
		return nil
	}
	var asNumber float64
	if err := json.Unmarshal(data, &asNumber); err != nil {
		return err
	}
	*f = flexiblePriceString(strconv.FormatFloat(asNumber, 'f', -1, 64))
	return nil
}
