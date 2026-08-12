package extractor

import "testing"

func TestGenericExtractsUniqloProductPriceBeforeBroadRegex(t *testing.T) {
	body := []byte(`<html><head><title>Wide Pants</title></head><body><script>window.__PRELOADED_STATE__ = {"noise":"z2","entity":{"product":{"productId":"E479620-000","prices":{"base":{"currency":{"code":"INR","symbol":"₹"},"value":2490},"promo":null}}}};</script></body></html>`)
	result, err := NewGeneric().Extract(body, "https://www.uniqlo.com/in/en/products/E479620-000/00?colorDisplayCode=09&sizeDisplayCode=007")
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Candidates) == 0 || result.Candidates[0].Price != "2490.00" || result.Candidates[0].Currency != "INR" || RuleType(result.Candidates[0].Rule) != "uniqlo_product_data" {
		t.Fatalf("expected exact Uniqlo product price, got %+v", result.Candidates)
	}
}

func TestGenericExtractsHebePromotionalPriceBeforeJSONLDRegularPrice(t *testing.T) {
	body := []byte(`<html><body>
<form data-product-gtm="{&quot;currency&quot;:&quot;PLN&quot;,&quot;regular_price&quot;:89.99,&quot;current_price&quot;:62.99}"></form>
<script type="application/ld+json">{"@context":"https://schema.org","@type":"Product","name":"Round Lab","offers":{"price":89.99,"priceCurrency":"PLN"}}</script>
</body></html>`)
	result, err := NewGeneric().Extract(body, "https://www.hebe.pl/product.html")
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Candidates) < 1 || result.Candidates[0].Price != "62.99" || RuleType(result.Candidates[0].Rule) != "hebe_gtm" {
		t.Fatalf("expected Hebe current price first, got %+v", result.Candidates)
	}
	if result.RegularPrice != "89.99" || result.DiscountPercent != 30 {
		t.Fatalf("expected regular price 89.99 and 30%% discount, got %+v", result)
	}
}

// WooCommerce's default Product schema nests the price under
// offers.priceSpecification.price rather than offers.price directly, and pages
// like supermaluch.com additionally emit a second, Yoast-style block wrapping the
// real product in a top-level "@graph" array. The extractor must not give up after
// the first script tag if it lacks a usable price — it should keep looking (and
// fall back to priceSpecification.price) until it finds one.
func TestGenericHandlesGraphWrappedJSONLDAndPriceSpecificationFallback(t *testing.T) {
	body := []byte(`<html><body>
<script type="application/ld+json">{"@context":"https://schema.org/","@type":"Product","name":"Wozek","offers":{"@type":"Offer","priceCurrency":"PLN","availability":"http://schema.org/InStock","priceSpecification":{"@type":"PriceSpecification","price":5099,"priceCurrency":"PLN"}}}</script>
<script type="application/ld+json">{"@context":"https://schema.org/","@graph":[{"@type":"BreadcrumbList"},{"@context":"https://schema.org/","@type":"Product","name":"Wozek","offers":[{"@type":"Offer","price":"5099.00","priceCurrency":"PLN","availability":"https://schema.org/InStock"}]}]}</script>
</body></html>`)
	result, err := NewGeneric().Extract(body, "https://www.supermaluch.com/produkt/wozek/")
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Candidates) != 1 || result.Candidates[0].Price != "5099.00" || RuleType(result.Candidates[0].Rule) != "json_ld" {
		t.Fatalf("expected a single JSON-LD price candidate of 5099.00, got %+v", result.Candidates)
	}
	if result.Candidates[0].Currency != "PLN" {
		t.Fatalf("expected currency PLN, got %+v", result.Candidates[0])
	}
}

func TestLamodaSPSNPageIsBotChallenge(t *testing.T) {
	body := []byte(`<script>function get_cookie_spsn() { return "spsn=123"; }</script>`)
	if !isBotChallenge(body) {
		t.Fatal("expected Lamoda SPSN page to be detected as bot challenge")
	}
}

// Some sites embed a "price" JSON field in minor units (e.g. grosze/cents) as a
// bare integer rather than a decimal amount. Without an alternate reading, a
// bare "22900" would be trusted as literally 22900, 100x too large.
func TestExtractByRegexOffersMinorUnitsReading(t *testing.T) {
	html := `<script>var data = {"price": "22900"};</script>`
	candidates := extractByRegex(html)

	var sawLiteral, sawMinorUnits bool
	for _, c := range candidates {
		if c.Price == "22900" {
			sawLiteral = true
		}
		if c.Price == "229.00" {
			sawMinorUnits = true
		}
	}
	if !sawLiteral {
		t.Error("expected the literal 22900 reading to still be offered")
	}
	if !sawMinorUnits {
		t.Error("expected a 229.00 minor-units reading to be offered alongside it")
	}
}

// A price already expressed with a decimal point is unambiguous and shouldn't
// get a second, incorrectly-rescaled candidate.
func TestExtractByRegexSkipsMinorUnitsWhenDecimalAlreadyPresent(t *testing.T) {
	html := `<script>var data = {"price": "229.00"};</script>`
	candidates := extractByRegex(html)

	for _, c := range candidates {
		if c.Label == "Regex JSON match (minor units)" {
			t.Errorf("did not expect a minor-units candidate for an already-decimal price, got %q", c.Price)
		}
	}
}
