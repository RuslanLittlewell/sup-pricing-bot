package shops

import "testing"

func TestParseHebePriceDiscounted(t *testing.T) {
	body := []byte(`<form data-product-gtm="{&quot;currency&quot;:&quot;PLN&quot;,&quot;price&quot;:62.99,&quot;regular_price&quot;:89.99,&quot;current_price&quot;:62.99}"></form>`)
	price, err := ParseHebePrice(body)
	if err != nil {
		t.Fatal(err)
	}
	if price.Current != 62.99 || price.Regular != 89.99 || price.DiscountPercent != 30 || price.Currency != "PLN" {
		t.Fatalf("unexpected Hebe price: %+v", price)
	}
}

func TestIsHebeURL(t *testing.T) {
	if !IsHebeURL("https://www.hebe.pl/product.html") {
		t.Fatal("expected hebe.pl URL")
	}
	if IsHebeURL("https://example.com/product.html") {
		t.Fatal("did not expect non-Hebe URL")
	}
}
