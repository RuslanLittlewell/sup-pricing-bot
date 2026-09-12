package shops

import "testing"

// Shape and values taken from a live www.supermaluch.com Store API response. The price is
// deliberately the real one: 589600 minor units is 5896.00 PLN, and reading it literally
// is the 100x error this parser exists to prevent.
const supermaluchStoreAPIResponse = `[{
	"id": 276154,
	"name": "Anex Modu – wózek wielofunkcyjny + dodatkowa spacerówka + dodatkowa gondola – Rooty",
	"slug": "anex-modu-wozek-wielofunkcyjny-dodatkowa-spacerowka-dodatkowa-gondola-rooty",
	"parent": 276157,
	"type": "simple",
	"permalink": "https://www.supermaluch.com/produkt/anex-modu-wozek-wielofunkcyjny-dodatkowa-spacerowka-dodatkowa-gondola-rooty/",
	"is_in_stock": true,
	"is_purchasable": true,
	"images": [{"src": "https://www.supermaluch.com/wp-content/uploads/Anex_Modu_Rooty.webp"}],
	"prices": {
		"price": "589600",
		"regular_price": "620000",
		"sale_price": "589600",
		"price_range": null,
		"currency_code": "PLN",
		"currency_minor_unit": 2,
		"currency_symbol": "zł"
	}
}]`

const supermaluchProductURL = "https://www.supermaluch.com/produkt/anex-modu-wozek-wielofunkcyjny-dodatkowa-spacerowka-dodatkowa-gondola-rooty/"

func TestParseWooStoreProductDecodesMinorUnits(t *testing.T) {
	product, err := ParseWooStoreProduct([]byte(supermaluchStoreAPIResponse), supermaluchProductURL)
	if err != nil {
		t.Fatal(err)
	}
	if product.Price != 5896.00 {
		t.Fatalf("price = %.2f, want 5896.00", product.Price)
	}
	if product.RegularPrice != 6200.00 {
		t.Fatalf("regular price = %.2f, want 6200.00", product.RegularPrice)
	}
	if product.Currency != "PLN" {
		t.Fatalf("currency = %q, want PLN", product.Currency)
	}
	if !product.InStock {
		t.Fatal("expected in stock")
	}
	if product.ImageURL == "" || product.Name == "" {
		t.Fatalf("expected name and image, got %q / %q", product.Name, product.ImageURL)
	}
}

func TestParseWooStoreProductDefaultsMinorUnitToTwo(t *testing.T) {
	// currency_minor_unit omitted entirely — the Store API contract still means minor
	// units, so treating the literal as a real amount would report 100x the price.
	body := []byte(`[{"slug":"widget","is_in_stock":true,"prices":{"price":"12900","currency_code":"EUR"}}]`)
	product, err := ParseWooStoreProduct(body, "https://shop.example/product/widget/")
	if err != nil {
		t.Fatal(err)
	}
	if product.Price != 129.00 {
		t.Fatalf("price = %.2f, want 129.00", product.Price)
	}
}

func TestParseWooStoreProductZeroMinorUnitCurrency(t *testing.T) {
	// JPY has no minor unit; 4980 really is ¥4980.
	body := []byte(`[{"slug":"widget","is_in_stock":true,"prices":{"price":"4980","currency_code":"JPY","currency_minor_unit":0}}]`)
	product, err := ParseWooStoreProduct(body, "https://shop.example/product/widget/")
	if err != nil {
		t.Fatal(err)
	}
	if product.Price != 4980 {
		t.Fatalf("price = %.2f, want 4980", product.Price)
	}
}

func TestParseWooStoreProductRejectsDifferentProduct(t *testing.T) {
	// ?slug= filters a collection rather than looking one up, so a miss can come back as
	// an unrelated product. Tracking that one's price instead would be silent and wrong.
	body := []byte(`[{"slug":"some-other-stroller","is_in_stock":true,"prices":{"price":"19900","currency_code":"PLN","currency_minor_unit":2}}]`)
	if _, err := ParseWooStoreProduct(body, supermaluchProductURL); err == nil {
		t.Fatal("expected an error when the API returns a different product")
	}
}

func TestParseWooStoreProductEmptyResponse(t *testing.T) {
	if _, err := ParseWooStoreProduct([]byte(`[]`), supermaluchProductURL); err == nil {
		t.Fatal("expected an error for an empty collection")
	}
}

func TestParseWooStoreProductOutOfStock(t *testing.T) {
	body := []byte(`[{"slug":"widget","is_in_stock":false,"prices":{"price":"12900","currency_code":"PLN","currency_minor_unit":2}}]`)
	product, err := ParseWooStoreProduct(body, "https://shop.example/produkt/widget/")
	if err != nil {
		t.Fatal(err)
	}
	if product.InStock {
		t.Fatal("expected out of stock")
	}
}

func TestParseWooStoreProductVariablePriceRange(t *testing.T) {
	body := []byte(`[{"slug":"widget","is_in_stock":true,"prices":{"price":"","price_range":{"min_amount":"15000","max_amount":"25000"},"currency_code":"PLN","currency_minor_unit":2}}]`)
	product, err := ParseWooStoreProduct(body, "https://shop.example/produkt/widget/")
	if err != nil {
		t.Fatal(err)
	}
	if product.Price != 150.00 {
		t.Fatalf("price = %.2f, want 150.00 (range floor)", product.Price)
	}
}

func TestWPAPIRootFromDiscoveryLink(t *testing.T) {
	body := []byte(`<html><head><link rel="https://api.w.org/" href="https://www.supermaluch.com/wp-json/" /></head></html>`)
	if got := WPAPIRoot(body); got != "https://www.supermaluch.com/wp-json/" {
		t.Fatalf("api root = %q", got)
	}
}

func TestWPAPIRootSubdirectoryInstallAndMissing(t *testing.T) {
	// Assuming "/wp-json/" would miss a subdirectory install entirely.
	body := []byte(`<link rel='https://api.w.org/' href='https://example.com/blog/wp-json/'>`)
	if got := WPAPIRoot(body); got != "https://example.com/blog/wp-json/" {
		t.Fatalf("api root = %q", got)
	}
	if got := WPAPIRoot([]byte(`<html><head><title>not wordpress</title></head></html>`)); got != "" {
		t.Fatalf("expected no api root, got %q", got)
	}
}

func TestWPAPIRootAddsTrailingSlash(t *testing.T) {
	body := []byte(`<link rel="https://api.w.org/" href="https://example.com/wp-json">`)
	if got := WPAPIRoot(body); got != "https://example.com/wp-json/" {
		t.Fatalf("api root = %q", got)
	}
}

func TestWooStoreProductsURL(t *testing.T) {
	got, ok := WooStoreProductsURL("https://www.supermaluch.com/wp-json/", supermaluchProductURL)
	if !ok {
		t.Fatal("expected a Store API URL")
	}
	want := "https://www.supermaluch.com/wp-json/wc/store/v1/products?slug=anex-modu-wozek-wielofunkcyjny-dodatkowa-spacerowka-dodatkowa-gondola-rooty"
	if got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
}

func TestWooStoreProductsURLRestRouteInstall(t *testing.T) {
	// Pretty permalinks off: the route travels in the query string, and "/wp-json/" 404s.
	got, ok := WooStoreProductsURL("https://example.com/?rest_route=/", "https://example.com/produkt/widget/")
	if !ok {
		t.Fatal("expected a Store API URL")
	}
	want := "https://example.com/?rest_route=%2Fwc%2Fstore%2Fv1%2Fproducts&slug=widget"
	if got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
}

func TestWooStoreProductsURLNeedsSlugAndRoot(t *testing.T) {
	if _, ok := WooStoreProductsURL("https://example.com/wp-json/", "https://example.com/"); ok {
		t.Fatal("expected failure for a URL with no product slug")
	}
	if _, ok := WooStoreProductsURL("", "https://example.com/produkt/widget/"); ok {
		t.Fatal("expected failure when the site advertises no REST root")
	}
}

func TestWooProductSlug(t *testing.T) {
	cases := map[string]string{
		supermaluchProductURL:                        "anex-modu-wozek-wielofunkcyjny-dodatkowa-spacerowka-dodatkowa-gondola-rooty",
		"https://shop.example/product/widget":        "widget",
		"https://shop.example/shop/cat/sub/widget/":  "widget",
		"https://shop.example/produkt/w%C3%B3zek/":   "wózek",
		"https://shop.example/produkt/widget/?utm=x": "widget",
		"https://shop.example/":                      "",
	}
	for rawURL, want := range cases {
		if got := WooProductSlug(rawURL); got != want {
			t.Errorf("WooProductSlug(%q) = %q, want %q", rawURL, got, want)
		}
	}
}

func TestIsWooCommercePage(t *testing.T) {
	if !IsWooCommercePage([]byte(`<link href="/wp-content/plugins/woocommerce/assets/css/woocommerce.css">`)) {
		t.Fatal("expected WooCommerce detection from the plugin asset path")
	}
	if !IsWooCommercePage([]byte(`<script>var wc_add_to_cart_params = {};</script>`)) {
		t.Fatal("expected WooCommerce detection from the cart JS global")
	}
	// WordPress without WooCommerce must not be treated as a shop.
	if IsWooCommercePage([]byte(`<html><body class="wp-singular"><p>/wp-content/themes/blog</p></body></html>`)) {
		t.Fatal("plain WordPress must not be detected as WooCommerce")
	}
}
