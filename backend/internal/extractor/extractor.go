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
	if strings.Contains(a, "out of stock") || strings.Contains(a, "outofstock") || strings.Contains(a, "https://schema.org/outofstock") {
		return "out_of_stock"
	}
	if strings.Contains(a, "limited") {
		return "in_stock"
	}
	return "unknown"
}
