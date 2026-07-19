package shops

import "testing"

func TestParseRossmannPriceIgnoresShippingAndUnitPrice(t *testing.T) {
	body := []byte(`<html><body>
		<script type="application/ld+json">{"@type":"Product","offers":{"@type":"Offer","price":189.99,"priceCurrency":"PLN","shippingDetails":{"shippingRate":{"value":9.99,"currency":"PLN"}}}}</script>
		<div class="product-info__col">
			<span data-testid="product-price"><span>189</span><span>,99</span><span> zł</span></span>
			<span data-testid="product-price-per-unit">100 ml = 633,30 zł</span>
			<span>Dostawa od 9,99 zł</span>
		</div>
	</body></html>`)
	price, err := ParseRossmannPrice(body)
	if err != nil {
		t.Fatal(err)
	}
	if price.Current != 189.99 || price.Currency != "PLN" {
		t.Fatalf("unexpected Rossmann price: %+v", price)
	}
}

func TestParseRossmannPriceRejectsJSONLDMismatch(t *testing.T) {
	body := []byte(`<html><body>
		<script type="application/ld+json">{"@type":"Product","offers":{"price":"189.99","priceCurrency":"PLN"}}</script>
		<span data-testid="product-price">9,99 zł</span>
	</body></html>`)
	if _, err := ParseRossmannPrice(body); err == nil {
		t.Fatal("expected mismatched DOM and JSON-LD prices to fail")
	}
}

func TestIsRossmannURL(t *testing.T) {
	if !IsRossmannURL("https://www.rossmann.pl/Produkt/example") {
		t.Fatal("expected rossmann.pl URL")
	}
	if IsRossmannURL("https://example.com/rossmann.pl") {
		t.Fatal("did not expect a non-Rossmann URL")
	}
}
