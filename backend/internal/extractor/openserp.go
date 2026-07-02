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
)

const openSERPTimeout = 45 * time.Second

// OpenSERPExtractor asks a self-hosted OpenSERP service to search for the product URL
// and reads a currency-tagged price from the returned snippet or extracted content.
type OpenSERPExtractor struct {
	baseURL    string
	apiKey     string
	engines    string
	httpClient *http.Client
}

// NewOpenSERP returns nil when OPEN_SERP_BASE_URL is not configured.
func NewOpenSERP() *OpenSERPExtractor {
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
	}
}

func (e *OpenSERPExtractor) Domain() string { return "*openserp" }

func (e *OpenSERPExtractor) Extract(_ []byte, pageURL string) (*ExtractionResult, error) {
	empty := &ExtractionResult{Candidates: []PriceCandidate{}}
	if e == nil {
		return empty, nil
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

	ctx, cancel := context.WithTimeout(context.Background(), openSERPTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "GET", endpoint.String(), nil)
	if err != nil {
		return nil, 0, fmt.Errorf("build openserp request: %w", err)
	}
	if e.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+e.apiKey)
		req.Header.Set("X-API-Key", e.apiKey)
	}

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
