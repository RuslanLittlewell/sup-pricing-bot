package shops

import "testing"

func TestParseUniqloPriceUsesRequestedProduct(t *testing.T) {
	body := []byte(`<script>window.__PRELOADED_STATE__ = {"recommendation":{"productId":"E111111-000","prices":{"base":{"currency":{"code":"INR"},"value":999}}},"entity":{"product":{"productId":"E479620-000","prices":{"base":{"currency":{"code":"INR","symbol":"₹"},"value":2490},"promo":null}}}};</script>`)
	price, err := ParseUniqloPrice(body, "https://www.uniqlo.com/in/en/products/E479620-000/00?colorDisplayCode=09&sizeDisplayCode=007")
	if err != nil {
		t.Fatal(err)
	}
	if price.Current != 2490 || price.Regular != 2490 || price.Currency != "INR" {
		t.Fatalf("unexpected Uniqlo price: %+v", price)
	}
}

func TestParseUniqloPromotionalPrice(t *testing.T) {
	body := []byte(`<script>window.__PRELOADED_STATE__ = {"productId":"E479620-000","prices":{"base":{"currency":{"code":"INR"},"value":2490},"promo":{"currency":{"code":"INR"},"value":1990}}};</script>`)
	price, err := ParseUniqloPrice(body, "https://uniqlo.com/in/en/products/E479620-000/00")
	if err != nil {
		t.Fatal(err)
	}
	if price.Current != 1990 || price.Regular != 2490 || price.DiscountPercent != 20 {
		t.Fatalf("unexpected promotional Uniqlo price: %+v", price)
	}
}
