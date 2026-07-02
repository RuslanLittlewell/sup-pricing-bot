package extractor

import "testing"

func TestParseOpenSERPResponseExtractsCurrencyPrice(t *testing.T) {
	body := []byte(`{
		"results": [
			{
				"title": "Koszulka domowa Argentyna 26",
				"url": "https://www.adidas.pl/koszulka-domowa-argentyna-26/JM8396.html",
				"snippet": "Koszulka domowa Argentyna 26 w cenie 439 zł."
			}
		]
	}`)

	result, err := parseOpenSERPResponse(body, "https://www.adidas.pl/koszulka-domowa-argentyna-26/JM8396.html")
	if err != nil {
		t.Fatalf("parseOpenSERPResponse returned error: %v", err)
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

func TestParseOpenSERPResponseUsesExtractedContent(t *testing.T) {
	body := []byte(`{
		"results": [
			{
				"title": "Product page",
				"url": "https://shop.example/item",
				"snippet": "No price in snippet",
				"extracted": {
					"title": "Product page",
					"content": "Current price: EUR 129.99"
				}
			}
		]
	}`)

	result, err := parseOpenSERPResponse(body, "https://shop.example/item")
	if err != nil {
		t.Fatalf("parseOpenSERPResponse returned error: %v", err)
	}
	if len(result.Candidates) != 1 {
		t.Fatalf("expected 1 candidate, got %d", len(result.Candidates))
	}
	if result.Candidates[0].Price != "129.99" {
		t.Fatalf("expected price 129.99, got %q", result.Candidates[0].Price)
	}
	if result.Candidates[0].Currency != "EUR" {
		t.Fatalf("expected currency EUR, got %q", result.Candidates[0].Currency)
	}
}
