package extractor

import "testing"

// Amazon localizes prices by delivery country, so a VPS outside Poland sees EUR.
// Attribute/generic extraction returns bare numbers, and the currency must come from the
// page text next to that exact price instead of the user's input currency.
func TestFillMissingCurrencyReadsCurrencyNextToPriceOnPage(t *testing.T) {
	body := []byte(`<div id="corePriceDisplay_desktop_feature_div">
<span class="a-offscreen">EUR&nbsp;30.54</span>
<span class="a-price a-text-price" data-a-strike="true"><span class="a-offscreen">EUR34.49</span></span>
</div>
<div class="carousel"><span class="a-offscreen">$2</span></div>
<p>Cena: <span class="value">1 299,99</span> <span class="currency">zł</span></p>`)
	result := &ExtractionResult{Candidates: []PriceCandidate{
		{Price: "34.49"},
		{Price: "30,54"},
		{Price: "1299.99"},
		{Price: "12.00"},
		{Price: "$1,007.00"},
		{Price: "45.00", Currency: "GBP"},
	}}

	FillMissingCurrency(result, body)

	want := []string{"EUR", "EUR", "PLN", "", "USD", "GBP"}
	for i, currency := range want {
		if result.Candidates[i].Currency != currency {
			t.Fatalf("candidate %d (%s): expected currency %q, got %q", i, result.Candidates[i].Price, currency, result.Candidates[i].Currency)
		}
	}
}

func TestFillMissingCurrencyIgnoresLongerNumbersContainingPrice(t *testing.T) {
	result := &ExtractionResult{Candidates: []PriceCandidate{{Price: "34.49"}}}

	FillMissingCurrency(result, []byte(`<span>EUR 134.49</span><span>34.495 zł</span>`))

	if result.Candidates[0].Currency != "" {
		t.Fatalf("expected no currency for a price that only appears inside other numbers, got %q", result.Candidates[0].Currency)
	}
}

func TestFillMissingCurrencyToleratesNilResult(t *testing.T) {
	FillMissingCurrency(nil, []byte(`EUR 1.00`))
}
