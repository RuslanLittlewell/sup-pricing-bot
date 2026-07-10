package extractor

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

const searxNGTimeout = 30 * time.Second

// SearXNGExtractor queries our private metasearch instance. It never trusts a fuzzy
// Allegro title match: the selected offerId must occur in the result URL or snippet.
type SearXNGExtractor struct {
	baseURL    string
	httpClient *http.Client
}

func NewSearXNG() *SearXNGExtractor {
	baseURL := strings.TrimRight(os.Getenv("SEARXNG_BASE_URL"), "/")
	if baseURL == "" {
		return nil
	}
	return &SearXNGExtractor{baseURL: baseURL, httpClient: &http.Client{Timeout: searxNGTimeout}}
}

func (e *SearXNGExtractor) Domain() string { return "*searxng" }

func (e *SearXNGExtractor) Extract(_ []byte, pageURL string) (*ExtractionResult, error) {
	empty := &ExtractionResult{Candidates: []PriceCandidate{}}
	if e == nil {
		return empty, nil
	}

	query := pageURL
	if offerID := allegroOfferID(pageURL); offerID != "" {
		query = fmt.Sprintf(`site:allegro.pl "%s"`, offerID)
	}
	endpoint, err := url.Parse(e.baseURL + "/search")
	if err != nil {
		return nil, fmt.Errorf("build searxng URL: %w", err)
	}
	params := endpoint.Query()
	params.Set("q", query)
	params.Set("format", "json")
	params.Set("language", "pl-PL")
	params.Set("safesearch", "0")
	endpoint.RawQuery = params.Encode()

	ctx, cancel := context.WithTimeout(context.Background(), searxNGTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("build searxng request: %w", err)
	}
	resp, err := e.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("searxng request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("searxng returned status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 2*1024*1024))
	if err != nil {
		return nil, fmt.Errorf("read searxng response: %w", err)
	}
	return parseSearXNGResponse(body, pageURL)
}

type searxNGResponse struct {
	Results []searxNGResult `json:"results"`
}

type searxNGResult struct {
	URL     string `json:"url"`
	Title   string `json:"title"`
	Content string `json:"content"`
}

func (r searxNGResult) searchableText() string {
	return strings.Join([]string{r.Title, r.Content, r.URL}, "\n")
}

func parseSearXNGResponse(body []byte, pageURL string) (*ExtractionResult, error) {
	empty := &ExtractionResult{Candidates: []PriceCandidate{}}
	var response searxNGResponse
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, fmt.Errorf("parse searxng response: %w", err)
	}
	for _, result := range response.Results {
		if !searxNGResultMatches(result, pageURL) {
			continue
		}
		price, currency, ok := extractCurrencyPrice(result.searchableText())
		if !ok {
			continue
		}
		rule, _ := json.Marshal(map[string]string{"type": "searxng_exact_offer"})
		return &ExtractionResult{
			Title: result.Title,
			Candidates: []PriceCandidate{{
				Price:      price,
				Currency:   currency,
				Confidence: 0.72,
				Label:      "SearXNG exact search result",
				SourceURL:  result.URL,
				Rule:       rule,
			}},
		}, nil
	}
	return empty, nil
}

func searxNGResultMatches(result searxNGResult, pageURL string) bool {
	if offerID := allegroOfferID(pageURL); offerID != "" {
		return IsAllegroURL(result.URL) && strings.Contains(result.searchableText(), offerID)
	}
	return SameURL(result.URL, pageURL)
}

func allegroOfferID(rawURL string) string {
	if !IsAllegroURL(rawURL) {
		return ""
	}
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	return parsed.Query().Get("offerId")
}
