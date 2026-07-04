package extractor

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/littlewell/price-tracker/internal/proxypool"
)

const openSERPTimeout = 45 * time.Second

// openSERPProxyRetryAttempts bounds how many times a request is retried with a freshly
// picked proxy (see setProxyHeader, Store.Pick) before giving up — a single dead/failing
// proxy shouldn't sink the whole extraction when the pool has other options. Retrying is
// pointless when e.proxies is nil (no pool wired up: every attempt would go out the same
// unproxied way), but harmless — the extra attempt(s) just repeat the identical request.
const openSERPProxyRetryAttempts = 2

// OpenSERPExtractor asks a self-hosted OpenSERP service to search for the product URL
// and reads a currency-tagged price from the returned snippet or extracted content.
type OpenSERPExtractor struct {
	baseURL    string
	apiKey     string
	engines    string
	httpClient *http.Client
	proxies    *proxypool.Store
}

// NewOpenSERP returns nil when OPEN_SERP_BASE_URL is not configured. proxies may be nil
// (no pool wired up yet) — each request then goes out on OpenSERP's own IP as before.
func NewOpenSERP(proxies *proxypool.Store) *OpenSERPExtractor {
	baseURL := strings.TrimRight(os.Getenv("OPEN_SERP_BASE_URL"), "/")
	if baseURL == "" {
		return nil
	}
	engines := os.Getenv("OPEN_SERP_ENGINES")
	if engines == "" {
		engines = "google,bing,duckduckgo"
	}
	return &OpenSERPExtractor{
		baseURL:    baseURL,
		apiKey:     os.Getenv("OPEN_SERP_API_KEY"),
		engines:    engines,
		httpClient: &http.Client{Timeout: openSERPTimeout},
		proxies:    proxies,
	}
}

// setProxyHeader asks the pool for a currently-alive proxy and, if one's available, tells
// OpenSERP to route this one request through it via X-Proxy-URL — OpenSERP must have
// allow_request_proxy_url enabled for this to take effect (see deploy/docker-compose.prod.yml's
// openserp service). Silently does nothing when no pool is wired up or the pool is empty,
// so a request always goes out one way or another.
func (e *OpenSERPExtractor) setProxyHeader(req *http.Request) {
	if e.proxies == nil {
		return
	}
	ctx, cancel := context.WithTimeout(req.Context(), 3*time.Second)
	defer cancel()
	picked, ok := e.proxies.Pick(ctx)
	if !ok {
		return
	}
	// http://, not picked.URL()'s socks5:// — OpenSERP rejects authenticated SOCKS
	// proxies outright (see PickedProxy.HTTPURL's doc comment).
	req.Header.Set("X-Proxy-URL", picked.HTTPURL())
}

func (e *OpenSERPExtractor) Domain() string { return "*openserp" }

func (e *OpenSERPExtractor) Extract(_ []byte, pageURL string) (*ExtractionResult, error) {
	empty := &ExtractionResult{Candidates: []PriceCandidate{}}
	if e == nil {
		return empty, nil
	}

	// Try reading the target page's own structured data first: OpenSERP's /extract
	// fetches with tls-client fingerprinting and a headless-render fallback, which gets
	// past bot protection that blocks our own fetcher, and its JSON-LD/OG-tag price is
	// far more reliable than regexing whatever text a search snippet happens to contain.
	if result, err := e.extractDirect(pageURL); err == nil && result != nil && len(result.Candidates) > 0 {
		return result, nil
	}

	body, status, err := e.doRequest(pageURL)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("openserp returned status %d", status)
	}
	return parseOpenSERPResponse(body, pageURL)
}

// extractDirect calls OpenSERP's /extract endpoint on pageURL itself and looks for a
// price in its parsed structured data (schema.org JSON-LD, then OpenGraph/Twitter Card
// product tags). Returns a nil result (not an error) when the page fetched fine but
// simply had no structured price to find, so the caller falls back to the search tier.
func (e *OpenSERPExtractor) extractDirect(pageURL string) (*ExtractionResult, error) {
	endpoint, err := url.Parse(e.baseURL + "/extract")
	if err != nil {
		return nil, fmt.Errorf("build openserp extract URL: %w", err)
	}
	q := endpoint.Query()
	q.Set("url", pageURL)
	q.Set("format", "json")
	q.Set("mode", "auto")
	endpoint.RawQuery = q.Encode()

	var body []byte
	var lastErr error
	for attempt := 1; attempt <= openSERPProxyRetryAttempts; attempt++ {
		body, lastErr = e.doExtractAttempt(endpoint.String())
		if lastErr == nil {
			break
		}
	}
	if lastErr != nil {
		return nil, lastErr
	}

	var parsed openSERPExtractResult
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("parse openserp extract response: %w", err)
	}

	rule, _ := json.Marshal(map[string]string{"type": "openserp_extract"})

	for _, raw := range parsed.SchemaOrg {
		var obj map[string]interface{}
		if err := json.Unmarshal(raw, &obj); err != nil {
			continue
		}
		product := parseLDObject(obj)
		if product == nil || product.Offers.Price == "" {
			continue
		}
		return &ExtractionResult{
			Title: firstNonEmptyString(parsed.Title, product.Name),
			Candidates: []PriceCandidate{{
				Price:      product.Offers.Price,
				Currency:   product.Offers.PriceCurrency,
				Confidence: 0.9,
				Label:      "OpenSERP extract (JSON-LD)",
				SourceURL:  parsed.URL,
				Rule:       rule,
			}},
		}, nil
	}

	// OpenGraph's "product" object and Twitter's product Card use different key
	// prefixes for the same thing across sites — check both.
	if price := firstNonEmptyString(parsed.OGTags["product:price:amount"], parsed.OGTags["og:price:amount"]); price != "" {
		currency := firstNonEmptyString(parsed.OGTags["product:price:currency"], parsed.OGTags["og:price:currency"])
		return &ExtractionResult{
			Title: parsed.Title,
			Candidates: []PriceCandidate{{
				Price:      price,
				Currency:   currency,
				Confidence: 0.85,
				Label:      "OpenSERP extract (OG tag)",
				SourceURL:  parsed.URL,
				Rule:       rule,
			}},
		}, nil
	}

	return nil, nil
}

// doExtractAttempt performs one /extract call — pulled out of extractDirect so a retry
// (see openSERPProxyRetryAttempts) rebuilds the request from scratch, picking a fresh
// proxy via setProxyHeader rather than reusing one that may have just failed.
func (e *OpenSERPExtractor) doExtractAttempt(endpoint string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), openSERPTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "GET", endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("build openserp extract request: %w", err)
	}
	if e.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+e.apiKey)
		req.Header.Set("X-API-Key", e.apiKey)
	}
	e.setProxyHeader(req)

	resp, err := e.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("openserp extract request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("openserp extract returned status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read openserp extract response: %w", err)
	}
	return body, nil
}

func firstNonEmptyString(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

type openSERPExtractResult struct {
	URL       string            `json:"url"`
	Title     string            `json:"title"`
	SchemaOrg []json.RawMessage `json:"schema_org"`
	OGTags    map[string]string `json:"og_tags"`
}

func (e *OpenSERPExtractor) doRequest(pageURL string) ([]byte, int, error) {
	endpoint, err := url.Parse(e.baseURL + "/mega/search")
	if err != nil {
		return nil, 0, fmt.Errorf("build openserp URL: %w", err)
	}
	q := endpoint.Query()
	q.Set("text", pageURL)
	q.Set("engines", e.engines)
	q.Set("mode", "any")
	q.Set("limit", "5")
	q.Set("extract", "1")
	q.Set("lang", "PL")
	q.Set("region", "PL")
	endpoint.RawQuery = q.Encode()

	var body []byte
	var status int
	var lastErr error
	for attempt := 1; attempt <= openSERPProxyRetryAttempts; attempt++ {
		body, status, lastErr = e.doSearchAttempt(endpoint.String())
		if lastErr == nil {
			break
		}
	}
	return body, status, lastErr
}

// doSearchAttempt performs one /mega/search call — pulled out of doRequest so a retry
// (see openSERPProxyRetryAttempts) rebuilds the request from scratch, picking a fresh
// proxy via setProxyHeader rather than reusing one that may have just failed.
func (e *OpenSERPExtractor) doSearchAttempt(endpoint string) ([]byte, int, error) {
	ctx, cancel := context.WithTimeout(context.Background(), openSERPTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "GET", endpoint, nil)
	if err != nil {
		return nil, 0, fmt.Errorf("build openserp request: %w", err)
	}
	if e.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+e.apiKey)
		req.Header.Set("X-API-Key", e.apiKey)
	}
	e.setProxyHeader(req)

	resp, err := e.httpClient.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("openserp request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, 0, fmt.Errorf("read openserp response: %w", err)
	}
	return body, resp.StatusCode, nil
}

func parseOpenSERPResponse(body []byte, pageURL string) (*ExtractionResult, error) {
	empty := &ExtractionResult{Candidates: []PriceCandidate{}}

	var parsed openSERPResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("parse openserp response: %w", err)
	}

	match := bestMatchingOpenSERPResult(parsed.Results, pageURL)
	if match == nil {
		return empty, nil
	}

	price, currency, ok := extractCurrencyPrice(match.searchableText())
	if !ok {
		return empty, nil
	}

	rule, _ := json.Marshal(map[string]string{"type": "openserp_search_result"})
	return &ExtractionResult{
		Title: match.Title,
		Candidates: []PriceCandidate{{
			Price:      price,
			Currency:   currency,
			Confidence: 0.65,
			Label:      "OpenSERP search result",
			SourceURL:  match.URL,
			Rule:       rule,
		}},
	}, nil
}

func bestMatchingOpenSERPResult(results []openSERPResult, pageURL string) *openSERPResult {
	for i := range results {
		if SameURL(results[i].URL, pageURL) {
			return &results[i]
		}
	}
	return nil
}

func SameURL(a, b string) bool {
	pa, errA := url.Parse(a)
	pb, errB := url.Parse(b)
	if errA != nil || errB != nil {
		return strings.TrimRight(a, "/") == strings.TrimRight(b, "/")
	}
	pa.Fragment = ""
	pa.RawQuery = ""
	pb.Fragment = ""
	pb.RawQuery = ""
	pa.Host = strings.ToLower(pa.Host)
	pb.Host = strings.ToLower(pb.Host)
	return strings.TrimRight(pa.String(), "/") == strings.TrimRight(pb.String(), "/")
}

func (r openSERPResult) searchableText() string {
	return strings.Join([]string{r.Title, r.Snippet, r.Extracted.Title, r.Extracted.Content}, "\n")
}

func extractCurrencyPrice(text string) (string, string, bool) {
	patterns := []struct {
		re            *regexp.Regexp
		priceGroup    int
		currencyGroup int
	}{
		{regexp.MustCompile(`(?i)(PLN|zł|EUR|€|USD|\$|GBP|£|RUB|₽)\s*([0-9][0-9\s.,]*(?:[.,][0-9]{2})?)`), 2, 1},
		{regexp.MustCompile(`(?i)([0-9][0-9\s.,]*(?:[.,][0-9]{2})?)\s*(PLN|zł|EUR|€|USD|\$|GBP|£|RUB|₽)`), 1, 2},
	}
	for _, pattern := range patterns {
		if match := pattern.re.FindStringSubmatch(text); len(match) > pattern.priceGroup {
			price := strings.TrimSpace(match[pattern.priceGroup])
			if len(price) < 2 {
				continue
			}
			return price, normalizeCurrencySymbol(match[pattern.currencyGroup]), true
		}
	}
	return "", "", false
}

type openSERPResponse struct {
	Results []openSERPResult `json:"results"`
}

type openSERPResult struct {
	Title     string `json:"title"`
	URL       string `json:"url"`
	Snippet   string `json:"snippet"`
	Extracted struct {
		Title   string `json:"title"`
		Content string `json:"content"`
	} `json:"extracted"`
}
