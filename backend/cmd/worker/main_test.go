package main

import (
	"testing"

	"github.com/littlewell/price-tracker/internal/extractor"
)

func TestParsePriceTokensFromTextWithNarrowNoBreakSpace(t *testing.T) {
	prices := parsePriceTokensFromText("Cena 2\u202f699 zł")
	if len(prices) != 1 {
		t.Fatalf("expected 1 price, got %d", len(prices))
	}
	if prices[0] != 2699 {
		t.Fatalf("expected price 2699, got %v", prices[0])
	}
}

// A sibling discount block (crossed-out original price) can appear next to
// the tracked block between checks, shifting what sits at the recorded
// tokenIndex. The unchanged price should still win over a stale index.
func TestParsePriceFromTextAtIndexPrefersUnchangedPriceOverStaleIndex(t *testing.T) {
	referencePrice := 828.0
	tokenIndex := 0 // recorded back when the block only contained "1035"
	price, ok := parsePriceFromTextAtIndex("1035,00 zł 828,00 zł", &tokenIndex, &referencePrice)
	if !ok {
		t.Fatal("expected a price to be found")
	}
	if price != 828 {
		t.Fatalf("expected unchanged reference price 828, got %v", price)
	}
}

func TestParsePriceFromTextAtIndexPrefersSmallerPriceWhenNoReferenceMatches(t *testing.T) {
	tokenIndex := 0
	price, ok := parsePriceFromTextAtIndex("1035,00 zł 828,00 zł", &tokenIndex, nil)
	if !ok {
		t.Fatal("expected a price to be found")
	}
	if price != 828 {
		t.Fatalf("expected smaller price 828, got %v", price)
	}
}

func TestParsePriceFromTextAtIndexUsesTokenIndexWhenOnlyOnePriceFound(t *testing.T) {
	referencePrice := 500.0
	tokenIndex := 0
	price, ok := parsePriceFromTextAtIndex("199.00 zł", &tokenIndex, &referencePrice)
	if !ok {
		t.Fatal("expected a price to be found")
	}
	if price != 199 {
		t.Fatalf("expected the only price 199, got %v", price)
	}
}

// A recheck of a tracker created off an ambiguous "price in minor units" JSON field
// (see extractByRegex's regex_json / regex_json_minor_units candidates) must not
// mistake the raw, 100x-too-large reading for a real price change just because it
// happens to be listed first.
func TestBestPriceCandidatePrefersUnchangedPriceOverFirstCandidate(t *testing.T) {
	referencePrice := 229.0
	candidates := []extractor.PriceCandidate{
		{Price: "22900", Confidence: 0.4, Label: "Regex JSON match"},
		{Price: "229.00", Confidence: 0.35, Label: "Regex JSON match (minor units)"},
	}
	_, price, ok := bestPriceCandidate(candidates, &referencePrice)
	if !ok {
		t.Fatal("expected a price to be found")
	}
	if price != 229 {
		t.Fatalf("expected unchanged reference price 229, got %v", price)
	}
}

func TestBestPriceCandidatePrefersHighestConfidenceWhenNoReferenceMatches(t *testing.T) {
	candidates := []extractor.PriceCandidate{
		{Price: "22900", Confidence: 0.4, Label: "Regex JSON match"},
		{Price: "229.00", Confidence: 0.35, Label: "Regex JSON match (minor units)"},
		{Price: "199.00", Confidence: 0.95, Label: "JSON-LD price"},
	}
	_, price, ok := bestPriceCandidate(candidates, nil)
	if !ok {
		t.Fatal("expected a price to be found")
	}
	if price != 199 {
		t.Fatalf("expected highest-confidence price 199, got %v", price)
	}
}
