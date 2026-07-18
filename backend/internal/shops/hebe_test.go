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

func TestHasHebeAddToCartButtonForMainProduct(t *testing.T) {
	body := []byte(`<html><body>
		<div hidden>Powiadom mnie — produkt niedostępny</div>
		<button class="add-to-cart add-product-detail js-add-to-cart js-add-to-cart-initial" type="submit" title="DODAJ DO KOSZYKA" value="DODAJ DO KOSZYKA">
			<span>DODAJ DO KOSZYKA</span>
		</button>
	</body></html>`)
	if !HasHebeAddToCartButton(body) {
		t.Fatal("expected the main product add-to-cart button to mean in stock")
	}
}

func TestHasHebeAddToCartButtonIgnoresRecommendation(t *testing.T) {
	body := []byte(`<html><body>
		<button class="recommendation-tile__add-to-cart" title="DODAJ DO KOSZYKA">DODAJ DO KOSZYKA</button>
		<button class="add-product-detail js-add-to-cart-initial">Powiadom mnie</button>
	</body></html>`)
	if HasHebeAddToCartButton(body) {
		t.Fatal("did not expect a recommendation button to mark the main product in stock")
	}
}
