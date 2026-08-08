package shops

import "testing"

func TestIsZaraURL(t *testing.T) {
	cases := map[string]bool{
		"https://www.zara.com/pl/pl/skorzana-kurtka-bomberka-p05479065.html": true,
		"https://zara.com/us/en/foo.html":                                    true,
		"https://www.massimodutti.com/pl/p-123.html":                         false,
		"not a url": false,
	}
	for url, want := range cases {
		if got := IsZaraURL(url); got != want {
			t.Errorf("IsZaraURL(%q) = %v, want %v", url, got, want)
		}
	}
}

func TestParseZaraSizes(t *testing.T) {
	html := `<html><head>
<script type="application/ld+json">
{"@context":"https://schema.org/","@type":"ProductGroup","hasVariant":[
  {"@type":"Product","size":"XS","sku":"1-1","offers":{"@type":"Offer","availability":"https://schema.org/InStock","price":"299","priceCurrency":"PLN"}},
  {"@type":"Product","size":"S","sku":"1-2","offers":{"@type":"Offer","availability":"https://schema.org/InStock","price":"299","priceCurrency":"PLN"}},
  {"@type":"Product","size":"L","sku":"1-4","offers":{"@type":"Offer","availability":"https://schema.org/OutOfStock","price":"299","priceCurrency":"PLN"}}
]}
</script>
</head><body></body></html>`

	variants, err := ParseZaraSizes([]byte(html))
	if err != nil {
		t.Fatalf("ParseZaraSizes failed: %v", err)
	}
	if len(variants) != 3 {
		t.Fatalf("expected 3 variants, got %d", len(variants))
	}
	if variants[0].Size != "XS" || !variants[0].InStock {
		t.Errorf("expected XS in stock, got %+v", variants[0])
	}
	if variants[2].Size != "L" || variants[2].InStock {
		t.Errorf("expected L out of stock, got %+v", variants[2])
	}
}

func TestParseZaraSizesNoJSONLD(t *testing.T) {
	if _, err := ParseZaraSizes([]byte("<html><body>no data here</body></html>")); err == nil {
		t.Fatal("expected an error when no ProductGroup JSON-LD is present")
	}
}

func TestParseZaraPriceDiscounted(t *testing.T) {
	// Mirrors the real markup of a marked-down Zara PDP: original price, 30-day-low,
	// then the actual current (lowest) price — in that document order.
	html := `<span class="price-old__amount"><data data-currency="PLN" value="899.00"><span class="money-amount__main">899,00 PLN</span></data></span>` +
		`<del><data data-currency="PLN" value="599.00"><span class="money-amount__main">599,00 PLN</span></data></del>` +
		`<ins class="price-current"><data data-currency="PLN" value="299.00"><span class="money-amount__main">299,00 PLN</span></data></ins>`

	price, currency, ok := ParseZaraPrice([]byte(html))
	if !ok {
		t.Fatal("expected a price to be found")
	}
	if price != "299.00" || currency != "PLN" {
		t.Errorf("ParseZaraPrice = (%q, %q), want (\"299.00\", \"PLN\")", price, currency)
	}
}

func TestParseZaraPriceSingle(t *testing.T) {
	html := `<data data-currency="EUR" value="49.95"><span class="money-amount__main">49,95 EUR</span></data>`
	price, currency, ok := ParseZaraPrice([]byte(html))
	if !ok || price != "49.95" || currency != "EUR" {
		t.Errorf("ParseZaraPrice = (%q, %q, %v), want (\"49.95\", \"EUR\", true)", price, currency, ok)
	}
}

func TestParseZaraPriceNone(t *testing.T) {
	if _, _, ok := ParseZaraPrice([]byte("<html><body>no price here</body></html>")); ok {
		t.Fatal("expected no price to be found")
	}
}

func TestIsZaraProductPage(t *testing.T) {
	// A fully sold-out PDP still renders the size-selector wrapper (holding the
	// "similar products / OUT OF STOCK" button instead of a size list), so the marker has to
	// hold for it too — that's the case the sold-out fallback depends on.
	soldOut := []byte(`<div class="product-detail-size-selector-std product-detail-info__size-selector">` +
		`<div class="product-detail-size-selector-std__wrapper"><button data-qa-action="show-similar-products">` +
		`<span class="zds-button__second-line"><span>OUT OF STOCK</span></span></button></div></div>`)
	if !IsZaraProductPage(soldOut) {
		t.Error("a sold-out Zara PDP must still be recognised as a product page")
	}

	// Akamai's interstitial, verbatim from a blocked request. It carries no challenge marker
	// once rendered, so without this check it reaches the parsers as if it were the product.
	interstitial := []byte(`<!DOCTYPE html><html><head><meta http-equiv="refresh" content="5; URL='/pl/en/x.html'" />` +
		`<title>&nbsp;</title></head><body><iframe src="/interstitial/ic.html"></iframe></body></html>`)
	if IsZaraProductPage(interstitial) {
		t.Error("an Akamai interstitial must not pass as a Zara product page")
	}
}
