package handler

import (
	"encoding/json"
	"testing"

	"github.com/littlewell/price-tracker/internal/extractor"
)

func TestFoundPriceState(t *testing.T) {
	const url = "https://shop.example/product"
	direct := extractor.PriceCandidate{Price: "234.99", Currency: "USD", Rule: json.RawMessage(`{"type":"json_ld","path":"offers.price"}`)}
	tests := []struct {
		name   string
		change func(*extractor.PriceCandidate)
		want   bool
	}{
		{"different website price", func(c *extractor.PriceCandidate) {}, true},
		{"unrelated product", func(c *extractor.PriceCandidate) { c.SourceURL = "https://shop.example/other" }, false},
		{"search without source", func(c *extractor.PriceCandidate) { c.Rule = json.RawMessage(`{"type":"serpapi_rich_snippet"}`) }, false},
		{"search for same product", func(c *extractor.PriceCandidate) {
			c.Rule = json.RawMessage(`{"type":"serpapi_rich_snippet"}`)
			c.SourceURL = url
		}, true},
		{"missing rule", func(c *extractor.PriceCandidate) { c.Rule = nil }, false},
		{"invalid price", func(c *extractor.PriceCandidate) { c.Price = "unavailable" }, false},
		{"zero price", func(c *extractor.PriceCandidate) { c.Price = "0" }, false},
		{"sub-cent price", func(c *extractor.PriceCandidate) { c.Price = "0.001" }, false},
		{"currency fallback", func(c *extractor.PriceCandidate) { c.Currency = "" }, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			candidate := direct
			tt.change(&candidate)
			state, ok := foundPriceState(url, "USD", &extractor.ExtractionResult{Title: "Product", Candidates: []extractor.PriceCandidate{candidate}})
			if ok != tt.want {
				t.Fatalf("got ok=%v, want %v", ok, tt.want)
			}
			if ok && (state.InitialPrice != 234.99 || state.Currency != "USD" || state.Step != "awaiting_found_price_confirm" || state.URL != url || state.Title != "Product" || string(state.Rule) != string(candidate.Rule)) {
				t.Fatalf("incorrect confirmation state: %+v", state)
			}
		})
	}
	if pricesMatchUserInput(234.99, 235.00) {
		t.Fatal("price mismatch must require confirmation")
	}
}

func TestFoundPriceStateSkipsUnusableCandidates(t *testing.T) {
	if _, ok := foundPriceState("https://shop.example/product", "USD", nil); ok {
		t.Fatal("nil result must not offer a price")
	}
	result := &extractor.ExtractionResult{Candidates: []extractor.PriceCandidate{
		{Price: "invalid", Rule: json.RawMessage(`{"type":"json_ld"}`)},
		{Price: "234.99", Rule: json.RawMessage(`{"type":"json_ld"}`)},
	}}
	state, ok := foundPriceState("https://shop.example/product", "USD", result)
	if !ok || state.InitialPrice != 234.99 {
		t.Fatalf("expected second candidate, got %+v, %v", state, ok)
	}
}
