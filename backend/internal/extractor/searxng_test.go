package extractor

import "testing"

func TestParseSearXNGExactAllegroOffer(t *testing.T) {
	body := []byte(`{"results":[{"url":"https://allegro.pl/produkt/ps5?offerId=18053959626","title":"PlayStation 5 Pro 4099 zł","content":"Oferta 18053959626"}]}`)
	result, err := parseSearXNGResponse(body, "https://allegro.pl/produkt/ps5?offerId=18053959626")
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Candidates) != 1 || result.Candidates[0].Price != "4099" {
		t.Fatalf("unexpected result: %#v", result)
	}
	if RuleType(result.Candidates[0].Rule) != "searxng_exact_offer" {
		t.Fatalf("unexpected rule: %s", result.Candidates[0].Rule)
	}
}

func TestParseSearXNGRejectsDifferentAllegroOffer(t *testing.T) {
	body := []byte(`{"results":[{"url":"https://allegro.pl/produkt/ps5?offerId=999","title":"PlayStation 5 Pro 3999 zł","content":"A similar offer"}]}`)
	result, err := parseSearXNGResponse(body, "https://allegro.pl/produkt/ps5?offerId=18053959626")
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Candidates) != 0 {
		t.Fatalf("expected inexact offer to be rejected: %#v", result.Candidates)
	}
}
