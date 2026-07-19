package shops

import "testing"

func TestParseNikeSizesUsesInventoryAvailability(t *testing.T) {
	page := []byte(`<html><script type="application/json">{"props":{"product":{"styleColor":"IR1997-100","sizes":[{"localizedLabel":"42","merchSkuId":"sku-42","gtins":[{"gtin":"gtin-42"}]},{"localizedLabel":"43","merchSkuId":"sku-43","gtins":[{"gtin":"gtin-43"}]}]}}}</script></html>`)
	inventory := []byte(`{"objects":[{"gtin":"gtin-42","method":"SHIP","available":true},{"gtin":"gtin-43","method":"SHIP","available":false}]}`)
	variants, err := ParseNikeSizes(page, inventory, "IR1997-100")
	if err != nil {
		t.Fatal(err)
	}
	if len(variants) != 2 || variants[0].Size != "EU 42" || !variants[0].InStock || variants[1].InStock {
		t.Fatalf("unexpected Nike variants: %+v", variants)
	}
}

func TestNikeAvailabilityAPIURL(t *testing.T) {
	apiURL, err := NikeAvailabilityAPIURL("https://www.nike.com/pl/t/product/IR1997-100?x=1")
	if err != nil || apiURL == "" {
		t.Fatalf("unexpected Nike API URL: %q, %v", apiURL, err)
	}
	if apiURL != "https://api.nike.com/deliver/available_gtins/v3?filter=styleColor(IR1997-100)&filter=merchGroup(EU)" {
		t.Fatalf("Nike API requires literal filter parentheses, got %q", apiURL)
	}
}

func TestParseNikeGTINAvailabilityMissing(t *testing.T) {
	_, err := ParseNikeGTINAvailability([]byte(`{"objects":[{"gtin":"other","available":true}]}`), "wanted")
	if err == nil {
		t.Fatal("expected missing GTIN to fail")
	}
}

func TestParseNikePriceUsesRequestedStyleColor(t *testing.T) {
	body := []byte(`<html><script type="application/json">{"props":{"products":[{"styleColor":"OTHER-001","prices":{"currency":"PLN","currentPrice":99.99,"initialPrice":99.99}},{"styleColor":"IR1997-100","prices":{"currency":"PLN","currentPrice":549.99,"initialPrice":779.99}}]}}</script></html>`)
	price, err := ParseNikePrice(body, "IR1997-100")
	if err != nil {
		t.Fatal(err)
	}
	if price.Current != 549.99 || price.Initial != 779.99 || price.Currency != "PLN" || price.DiscountPercent != 29 {
		t.Fatalf("unexpected Nike price: %+v", price)
	}
}
