package extractor

import "testing"

func TestParseSerperResponseExtractsOrganicAttributePrice(t *testing.T) {
	body := []byte(`{
		"organic": [
			{
				"title": "adidas Koszulka domowa Argentyna 26",
				"link": "https://www.adidas.pl/koszulka-domowa-argentyna-26/JM8396.html",
				"snippet": "Kup Koszulka domowa Argentyna 26 w oficjalnym sklepie adidas.",
				"attributes": {
					"Cena": "439 zł"
				}
			}
		]
	}`)

	result, err := parseSerperResponse(body, "https://www.adidas.pl/koszulka-domowa-argentyna-26/JM8396.html")
	if err != nil {
		t.Fatalf("parseSerperResponse returned error: %v", err)
	}
	if len(result.Candidates) != 1 {
		t.Fatalf("expected 1 candidate, got %d", len(result.Candidates))
	}
	if result.Candidates[0].Price != "439" {
		t.Fatalf("expected price 439, got %q", result.Candidates[0].Price)
	}
	if result.Candidates[0].Currency != "PLN" {
		t.Fatalf("expected currency PLN, got %q", result.Candidates[0].Currency)
	}
}

func TestParseSerperResponseExtractsPriceRangeWithTrackingParams(t *testing.T) {
	body := []byte(`{
		"organic": [
			{
				"title": "Tanie playstation 5 - ceny i opinie w Media Expert",
				"link": "https://www.mediaexpert.pl/gaming/playstation-5/konsole-ps5?sort=price_asc&srsltid=tracking",
				"snippet": "Cena konsoli PlayStation 5 jest uzależniona od wybranej wersji.",
				"priceRange": "2489,00 zł"
			}
		]
	}`)

	result, err := parseSerperResponse(body, "https://www.mediaexpert.pl/gaming/playstation-5/konsole-ps5")
	if err != nil {
		t.Fatalf("parseSerperResponse returned error: %v", err)
	}
	if len(result.Candidates) != 1 {
		t.Fatalf("expected 1 candidate, got %d", len(result.Candidates))
	}
	if result.Candidates[0].Price != "2489,00" {
		t.Fatalf("expected price 2489,00, got %q", result.Candidates[0].Price)
	}
	if result.Candidates[0].Currency != "PLN" {
		t.Fatalf("expected currency PLN, got %q", result.Candidates[0].Currency)
	}
}

func TestParseSerperResponseExtractsShoppingPrice(t *testing.T) {
	body := []byte(`{
		"shopping": [
			{
				"title": "Koszulka domowa Argentyna 26",
				"link": "https://www.adidas.pl/koszulka-domowa-argentyna-26/JM8396.html",
				"source": "adidas.pl",
				"price": "439 zł"
			}
		]
	}`)

	result, err := parseSerperResponse(body, "https://www.adidas.pl/koszulka-domowa-argentyna-26/JM8396.html")
	if err != nil {
		t.Fatalf("parseSerperResponse returned error: %v", err)
	}
	if len(result.Candidates) != 1 {
		t.Fatalf("expected 1 candidate, got %d", len(result.Candidates))
	}
	if result.Candidates[0].Price != "439" {
		t.Fatalf("expected price 439, got %q", result.Candidates[0].Price)
	}
	if result.Candidates[0].Currency != "PLN" {
		t.Fatalf("expected currency PLN, got %q", result.Candidates[0].Currency)
	}
}
