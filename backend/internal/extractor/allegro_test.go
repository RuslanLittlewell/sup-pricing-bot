package extractor

import "testing"

func TestExtractAllegroOfferMatchesOfferID(t *testing.T) {
	html := []byte(`<html><script>dataLayer=[{"idItem":"other","offerName":"Wrong","price":10,"currency":"PLN","active":"yes"},{"idItem":"18505984601","offerName":"MPM MOD-49","price":563.31,"currency":"PLN","active":"yes"}]</script></html>`)
	result := extractAllegroOffer(html, "https://allegro.pl/produkt/vacuum?id=x&offerId=18505984601")
	if result == nil || len(result.Candidates) != 1 {
		t.Fatalf("expected one Allegro candidate, got %#v", result)
	}
	if result.Candidates[0].Price != "563.31" || result.Candidates[0].Currency != "PLN" {
		t.Fatalf("unexpected candidate: %#v", result.Candidates[0])
	}
	if RuleType(result.Candidates[0].Rule) != "allegro_offer" {
		t.Fatalf("unexpected rule: %s", result.Candidates[0].Rule)
	}
	if result.Title != "MPM MOD-49" || result.StockStatus != "in_stock" {
		t.Fatalf("unexpected product data: %#v", result)
	}
}

func TestExtractAllegroOfferRejectsDifferentOffer(t *testing.T) {
	html := []byte(`<script>dataLayer=[{"idItem":"123","price":563.31,"currency":"PLN"}]</script>`)
	if result := extractAllegroOffer(html, "https://allegro.pl/produkt/x?offerId=456"); result != nil {
		t.Fatalf("expected no match, got %#v", result)
	}
}

func TestExtractAllegroSerializedOffer(t *testing.T) {
	html := []byte(`<script type="application/json">{"offerId":"18053959626","name":"PlayStation 5 Pro","price":{"priceString":"4 099,00 zł"}}</script>`)
	result := extractAllegroOffer(html, "https://allegro.pl/produkt/ps5?offerId=18053959626")
	if result == nil || len(result.Candidates) != 1 {
		t.Fatalf("expected serialized offer candidate, got %#v", result)
	}
	if result.Candidates[0].Price != "4099" || result.Title != "PlayStation 5 Pro" {
		t.Fatalf("unexpected serialized offer result: %#v", result)
	}
}

func TestGenericDoesNotParseAllegroChallengeTokens(t *testing.T) {
	result, err := NewGeneric().Extract([]byte(`<html><script>window.x={"price":"z1"}</script><p>Delivery 10 zł</p></html>`), "https://allegro.pl/produkt/x?offerId=123")
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Candidates) != 0 {
		t.Fatalf("expected no generic Allegro candidates, got %#v", result.Candidates)
	}
}
