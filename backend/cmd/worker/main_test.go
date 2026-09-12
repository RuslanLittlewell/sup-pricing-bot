package main

import (
	"errors"
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

// priceFromSelectorText is shared between the headless-render path (priceFromCSSRule)
// and the static-fetch path (priceFromStaticCSSRule) — this exercises the shared logic
// directly against a fake selector lookup, without needing a browser or network.
func TestPriceFromSelectorTextReadsPrimarySelector(t *testing.T) {
	rule := cssTextRule{Selector: "strong#price"}
	price, err := priceFromSelectorText(rule, nil, func(selector string) (string, error) {
		if selector != rule.Selector {
			t.Fatalf("unexpected selector %q", selector)
		}
		return "4 716,80 zł", nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if price != 4716.80 {
		t.Fatalf("expected 4716.80, got %v", price)
	}
}

// When the primary selector's price is unchanged from the reference, a wider
// screenshot_selector scan should still surface a lower (sale) price sitting
// alongside it, same as the render path does.
func TestPriceFromSelectorTextFindsSalePriceInScreenshotBlock(t *testing.T) {
	referencePrice := 1035.0
	rule := cssTextRule{Selector: "span.price", ScreenshotSelector: "div.price-block"}
	price, err := priceFromSelectorText(rule, &referencePrice, func(selector string) (string, error) {
		if selector == rule.Selector {
			return "1035,00 zł", nil
		}
		return "1035,00 zł 828,00 zł", nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if price != 828 {
		t.Fatalf("expected sale price 828, got %v", price)
	}
}

func TestPriceFromSelectorTextErrorsWhenSelectorLookupFails(t *testing.T) {
	rule := cssTextRule{Selector: "strong#price"}
	_, err := priceFromSelectorText(rule, nil, func(selector string) (string, error) {
		return "", errors.New("selector not found")
	})
	if err == nil {
		t.Fatal("expected an error when the selector can't be resolved")
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

func TestIsSearchFallbackRuleTypeCoversEveryTier(t *testing.T) {
	// Every tier in extractor.NewSearchFallback's chain must be recognised here: the rule
	// type is what tells the worker a tracker's stored rule came from a search API rather
	// than from reading the page, and a missed one would leave that tracker's stale rule
	// in place forever.
	for _, ruleType := range []string{
		"openserp_search_result", "openserp_extract", "searxng_exact_offer",
		"serper_organic_result", "serper_shopping_result", "serpapi_rich_snippet",
		"gemini_url_context",
	} {
		if !isSearchFallbackRuleType(ruleType) {
			t.Errorf("%s must be recognised as a search-fallback rule type", ruleType)
		}
	}
	// Methods that read the page (or the shop's own API) directly must not be.
	for _, ruleType := range []string{
		"json_ld", "css_text", "dom_attribute", "woocommerce_store_api",
		"wildberries_price", "zara_price", "",
	} {
		if isSearchFallbackRuleType(ruleType) {
			t.Errorf("%s must not be treated as a search-fallback rule type", ruleType)
		}
	}
}

func TestStaleSearchFallbackRuleIsClearedOnlyWhenResolvedCheaper(t *testing.T) {
	// Mirrors the condition in processTracker: the stored rule is dropped once a cheaper
	// tier resolves the tracker, and left alone while a search tier is still what works.
	shouldClear := func(stored, resolved string) bool {
		return isSearchFallbackRuleType(stored) && !isSearchFallbackRuleType(resolved)
	}
	cases := []struct {
		stored, resolved string
		want             bool
	}{
		{"openserp_extract", "woocommerce_store_api", true},
		{"serpapi_rich_snippet", "json_ld", true},
		{"openserp_extract", "serpapi_rich_snippet", false}, // still only search works
		{"css_text", "json_ld", false},                      // never was a search tracker
		{"", "woocommerce_store_api", false},
	}
	for _, tc := range cases {
		if got := shouldClear(tc.stored, tc.resolved); got != tc.want {
			t.Errorf("stored=%q resolved=%q: clear=%v, want %v", tc.stored, tc.resolved, got, tc.want)
		}
	}
}
