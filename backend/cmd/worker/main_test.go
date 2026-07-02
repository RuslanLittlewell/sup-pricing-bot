package main

import "testing"

func TestParsePriceTokensFromTextWithNarrowNoBreakSpace(t *testing.T) {
	prices := parsePriceTokensFromText("Cena 2\u202f699 zł")
	if len(prices) != 1 {
		t.Fatalf("expected 1 price, got %d", len(prices))
	}
	if prices[0] != 2699 {
		t.Fatalf("expected price 2699, got %v", prices[0])
	}
}
