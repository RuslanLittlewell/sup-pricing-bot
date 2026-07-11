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

func TestPricesMatchUserInputByWholeNumber(t *testing.T) {
	cases := []struct {
		found, entered float64
		want           bool
	}{
		{420.99, 420, true},
		{420.99, 420.1, true},
		{420.99, 420.99, true},
		{421.01, 420.99, false},
		{709.00531, 709.00, true},
	}
	for _, tc := range cases {
		if got := pricesMatchUserInput(tc.found, tc.entered); got != tc.want {
			t.Errorf("pricesMatchUserInput(%v, %v) = %v, want %v", tc.found, tc.entered, got, tc.want)
		}
	}
}

func TestNormalizePricePrecisionDropsExtraDigits(t *testing.T) {
	if got := normalizePricePrecision(709.00531); got != 709.00 {
		t.Fatalf("normalizePricePrecision = %.5f, want 709.00", got)
	}
}
