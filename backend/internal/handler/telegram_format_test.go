package handler

import "testing"

func TestParsePriceInputWithNarrowNoBreakSpace(t *testing.T) {
	price, currency, ok := parsePriceInput("2\u202f699")
	if !ok {
		t.Fatal("expected price to parse")
	}
	if price != 2699 {
		t.Fatalf("expected price 2699, got %v", price)
	}
	if currency != "PLN" {
		t.Fatalf("expected currency PLN, got %q", currency)
	}
}

func TestParsePriceInputWithNoBreakSpaceAndCurrency(t *testing.T) {
	price, currency, ok := parsePriceInput("2\u00a0699,99 zł")
	if !ok {
		t.Fatal("expected price to parse")
	}
	if price != 2699.99 {
		t.Fatalf("expected price 2699.99, got %v", price)
	}
	if currency != "PLN" {
		t.Fatalf("expected currency PLN, got %q", currency)
	}
}
