package extractor

import (
	"encoding/json"
	"strings"
)

type PriceCandidate struct {
	Price      string          `json:"price"`
	Currency   string          `json:"currency"`
	Confidence float64         `json:"confidence"`
	Label      string          `json:"label"`
	SourceURL  string          `json:"source_url,omitempty"`
	Rule       json.RawMessage `json:"rule"`
}

type ExtractionResult struct {
	Title       string           `json:"title,omitempty"`
	ImageURL    string           `json:"image_url,omitempty"`
	StockStatus string           `json:"stock_status,omitempty"`
	Candidates  []PriceCandidate `json:"candidates"`
}

type Extractor interface {
	Extract(html []byte, url string) (*ExtractionResult, error)
	Domain() string
}

// RuleType pulls the "type" field out of a PriceCandidate's Rule (e.g. "json_ld",
// "dom_attribute", "serpapi_rich_snippet"), for logging which extraction method resolved
// a given price. Returns "" if rule is empty or has no type field.
func RuleType(rule json.RawMessage) string {
	if len(rule) == 0 {
		return ""
	}
	var parsed struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(rule, &parsed); err != nil {
		return ""
	}
	return parsed.Type
}

type jsonLDProduct struct {
	Name   string `json:"name"`
	Image  string `json:"image"`
	Offers struct {
		Price         string `json:"price"`
		PriceCurrency string `json:"priceCurrency"`
		Availability  string `json:"availability"`
	} `json:"offers"`
}

func mapAvailability(avail string) string {
	a := strings.ToLower(avail)
	if strings.Contains(a, "in stock") || strings.Contains(a, "instock") || strings.Contains(a, "https://schema.org/instock") {
		return "in_stock"
	}
	if strings.Contains(a, "out of stock") || strings.Contains(a, "outofstock") || strings.Contains(a, "https://schema.org/outofstock") || strings.Contains(a, "soldout") {
		return "out_of_stock"
	}
	if strings.Contains(a, "limited") {
		return "in_stock"
	}
	return "unknown"
}
