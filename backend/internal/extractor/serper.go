package extractor

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

const (
	serperBaseURL = "https://google.serper.dev/search"
	serperTimeout = 45 * time.Second
)

// SerperExtractor uses Serper.dev's Google Search API as a fallback between OpenSERP
// and SerpAPI. It searches for the product URL and reads prices from organic snippets,
// organic attributes, or shopping result prices.
type SerperExtractor struct {
	apiKey     string
	httpClient *http.Client
}

// NewSerper returns nil when SERPER_API_KEY is not configured.
func NewSerper() *SerperExtractor {
	apiKey := os.Getenv("SERPER_API_KEY")
	if apiKey == "" {
		return nil
	}
	return &SerperExtractor{
		apiKey:     apiKey,
		httpClient: &http.Client{Timeout: serperTimeout},
	}
}

func (e *SerperExtractor) Domain() string { return "*serper" }

func (e *SerperExtractor) Extract(_ []byte, pageURL string) (*ExtractionResult, error) {
	empty := &ExtractionResult{Candidates: []PriceCandidate{}}
	if e == nil {
		return empty, nil
	}

	body, status, err := e.doRequest(pageURL)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("serper returned status %d", status)
	}
	return parseSerperResponse(body, pageURL)
}

func (e *SerperExtractor) doRequest(pageURL string) ([]byte, int, error) {
	payload, err := json.Marshal(map[string]string{
		"q":  pageURL,
		"gl": "pl",
		"hl": "pl",
	})
	if err != nil {
		return nil, 0, fmt.Errorf("build serper payload: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), serperTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "POST", serperBaseURL, bytes.NewReader(payload))
	if err != nil {
		return nil, 0, fmt.Errorf("build serper request: %w", err)
	}
	req.Header.Set("X-API-KEY", e.apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := e.httpClient.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("serper request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, 0, fmt.Errorf("read serper response: %w", err)
	}
	return body, resp.StatusCode, nil
}

func parseSerperResponse(body []byte, pageURL string) (*ExtractionResult, error) {
	empty := &ExtractionResult{Candidates: []PriceCandidate{}}

	var parsed serperResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("parse serper response: %w", err)
	}

	if result := bestMatchingSerperOrganic(parsed.Organic, pageURL); result != nil {
		if price, currency, ok := extractCurrencyPrice(result.searchableText()); ok {
			rule, _ := json.Marshal(map[string]string{"type": "serper_organic_result"})
			return &ExtractionResult{
				Title: result.Title,
				Candidates: []PriceCandidate{{
					Price:      price,
					Currency:   currency,
					Confidence: 0.7,
					Label:      "Serper organic result",
					SourceURL:  result.Link,
					Rule:       rule,
				}},
			}, nil
		}
	}

	if result := bestMatchingSerperShopping(parsed.Shopping, pageURL); result != nil {
		if price, currency, ok := extractCurrencyPrice(result.searchableText()); ok {
			rule, _ := json.Marshal(map[string]string{"type": "serper_shopping_result"})
			return &ExtractionResult{
				Title: result.Title,
				Candidates: []PriceCandidate{{
					Price:      price,
					Currency:   currency,
					Confidence: 0.65,
					Label:      "Serper shopping result",
					SourceURL:  result.Link,
					Rule:       rule,
				}},
			}, nil
		}
	}

	return empty, nil
}

func bestMatchingSerperOrganic(results []serperOrganicResult, pageURL string) *serperOrganicResult {
	for i := range results {
		if SameURL(results[i].Link, pageURL) {
			return &results[i]
		}
	}
	return nil
}

func bestMatchingSerperShopping(results []serperShoppingResult, pageURL string) *serperShoppingResult {
	for i := range results {
		if SameURL(results[i].Link, pageURL) {
			return &results[i]
		}
	}
	return nil
}

type serperResponse struct {
	Organic  []serperOrganicResult  `json:"organic"`
	Shopping []serperShoppingResult `json:"shopping"`
}

type serperOrganicResult struct {
	Title      string            `json:"title"`
	Link       string            `json:"link"`
	Snippet    string            `json:"snippet"`
	PriceRange string            `json:"priceRange"`
	Attributes map[string]string `json:"attributes"`
}

func (r serperOrganicResult) searchableText() string {
	parts := []string{r.Title, r.Snippet, r.PriceRange}
	for key, value := range r.Attributes {
		parts = append(parts, key, value)
	}
	return strings.Join(parts, "\n")
}

type serperShoppingResult struct {
	Title  string `json:"title"`
	Link   string `json:"link"`
	Source string `json:"source"`
	Price  string `json:"price"`
}

func (r serperShoppingResult) searchableText() string {
	return strings.Join([]string{r.Title, r.Source, r.Price}, "\n")
}
