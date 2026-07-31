package extractor

import (
	"strings"
	"testing"
)

func TestDetectStockStatusGenericKeywordScan(t *testing.T) {
	status, method, err := DetectStockStatus(nil, []byte("<html>Sorry, this item is out of stock</html>"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status != "out_of_stock" || method != "keyword_scan" {
		t.Fatalf("expected out_of_stock/keyword_scan, got %s/%s", status, method)
	}
}

func TestDetectStockStatusPolishNotifyMePhrase(t *testing.T) {
	status, method, err := DetectStockStatus(nil, []byte(`<html><button>Powiadom mnie</button></html>`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status != "out_of_stock" || method != "keyword_scan" {
		t.Fatalf("expected out_of_stock/keyword_scan, got %s/%s", status, method)
	}
}

func TestDetectStockStatusEmptyBodyErrors(t *testing.T) {
	// A degenerate (empty/truncated) response must not be read as in_stock — that produced
	// spurious "back in stock" notifications. It should surface as an extraction error.
	for _, body := range [][]byte{nil, []byte(""), []byte("   \n  "), []byte("<html></html>")} {
		if _, _, err := DetectStockStatus(nil, body); err == nil {
			t.Fatalf("expected error for degenerate body %q, got none", body)
		}
	}
}

func TestDetectStockStatusSubstantivePageInStock(t *testing.T) {
	// A real page with no out-of-stock phrase is our in_stock signal; the small-body guard
	// must not swallow it.
	body := []byte("<html><body>" + strings.Repeat("Makita DUR193Z podkaszarka ", 60) + "</body></html>")
	status, method, err := DetectStockStatus(nil, body)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status != "in_stock" || method != "keyword_scan" {
		t.Fatalf("expected in_stock/keyword_scan, got %s/%s", status, method)
	}
}

func TestDetectStockStatusZaraSizeInStock(t *testing.T) {
	rule := []byte(`{"type":"zara_size","size":"M","sku":"1-3"}`)
	body := []byte(`<html><head>
<script type="application/ld+json">
{"@context":"https://schema.org/","@type":"ProductGroup","hasVariant":[
  {"@type":"Product","size":"S","sku":"1-2","offers":{"@type":"Offer","availability":"https://schema.org/OutOfStock"}},
  {"@type":"Product","size":"M","sku":"1-3","offers":{"@type":"Offer","availability":"https://schema.org/InStock"}}
]}
</script>
</head></html>`)

	status, method, err := DetectStockStatus(rule, body)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status != "in_stock" || method != "json_ld" {
		t.Fatalf("expected in_stock/json_ld, got %s/%s", status, method)
	}
}

func TestDetectStockStatusZaraSizeNoLongerListed(t *testing.T) {
	rule := []byte(`{"type":"zara_size","size":"XXL","sku":"1-9"}`)
	body := []byte(`<html><head>
<script type="application/ld+json">
{"@context":"https://schema.org/","@type":"ProductGroup","hasVariant":[
  {"@type":"Product","size":"S","sku":"1-2","offers":{"@type":"Offer","availability":"https://schema.org/InStock"}}
]}
</script>
</head></html>`)

	if _, _, err := DetectStockStatus(rule, body); err == nil {
		t.Fatal("expected an error when the tracked size is no longer listed")
	}
}

func TestDetectStockStatusZaraSizeFullySoldOut(t *testing.T) {
	// When a Zara product is fully sold out, the page renders no size picker at all — no
	// ProductGroup/hasVariant JSON-LD — just a disabled button whose text says "out of
	// stock" in the page's locale (e.g. the zds-button__second-line span reading "НЕТ В
	// НАЛИЧИИ"). ParseZaraSizes can't find variant data, but that's a genuine out-of-stock
	// signal, not an extraction failure, so DetectStockStatus must resolve it rather than
	// erroring out (which previously drove the tracker to "needs_confirmation" after a few
	// consecutive checks instead of ever reporting the size as unavailable).
	rule := []byte(`{"type":"zara_size","size":"M","sku":"1-3"}`)
	body := []byte(`<html><body>` + strings.Repeat("Kurtka bomberka ", 40) +
		`<button><span class="zds-button__second-line">НЕТ В НАЛИЧИИ</span></button></body></html>`)

	status, method, err := DetectStockStatus(rule, body)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status != "out_of_stock" || method != "keyword_scan" {
		t.Fatalf("expected out_of_stock/keyword_scan, got %s/%s", status, method)
	}
}

func TestDetectStockStatusZaraSizeUnreadablePageStillErrors(t *testing.T) {
	// No variant JSON-LD AND no recognizable out-of-stock phrase (e.g. a broken/partial
	// render) must still surface as an error, not default to in_stock — same reasoning as
	// TestDetectStockStatusEmptyBodyErrors.
	rule := []byte(`{"type":"zara_size","size":"M","sku":"1-3"}`)
	body := []byte(`<html><body>` + strings.Repeat("Kurtka bomberka ", 40) + `</body></html>`)

	if _, _, err := DetectStockStatus(rule, body); err == nil {
		t.Fatal("expected an error when neither variant data nor an out-of-stock phrase is present")
	}
}

func TestDetectStockStatusWildberriesAPI(t *testing.T) {
	rule := []byte(`{"type":"wildberries_stock","article":"264041181"}`)
	body := []byte(`{"products":[{"id":264041181,"name":"Product","sizes":[{"stocks":[{"qty":2}],"price":{"product":10000,"logistics":500}}]}]}`)
	status, method, err := DetectStockStatus(rule, body)
	if err != nil {
		t.Fatal(err)
	}
	if status != "in_stock" || method != "wildberries_api" {
		t.Fatalf("got %s/%s", status, method)
	}
}

func TestDetectStockStatusNikeSize(t *testing.T) {
	rule := []byte(`{"type":"nike_size","size":"EU 42","gtin":"gtin-42"}`)
	body := []byte(`{"objects":[{"gtin":"gtin-42","method":"SHIP","available":true}]}`)
	status, method, err := DetectStockStatus(rule, body)
	if err != nil {
		t.Fatal(err)
	}
	if status != "in_stock" || method != "nike_inventory_api" {
		t.Fatalf("got %s/%s", status, method)
	}
}
