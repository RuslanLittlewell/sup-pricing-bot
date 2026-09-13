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

func TestDetectStockStatusItalianOutOfStockPhrase(t *testing.T) {
	// The false positive this test guards against: an it_it H&M page reporting in_stock
	// because the generic phrase list only covered English/Polish/Russian wording.
	status, method, err := DetectStockStatus(nil, []byte(`<html><body>`+strings.Repeat("Abito in misto lino con peplum ", 30)+`<button>Avvisami</button></body></html>`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status != "out_of_stock" || method != "keyword_scan" {
		t.Fatalf("expected out_of_stock/keyword_scan, got %s/%s", status, method)
	}
}

func TestDetectStockStatusHMSizeOutOfStock(t *testing.T) {
	rule := []byte(`{"type":"hm_size","size":"XS"}`)
	body := []byte(`<div data-testid="size-selector"><ul data-testid="grid">
<li><div id="sizeButton-0" data-testid="sizeButton-0" role="radio" aria-label="Taglia XS: esaurita. Seleziona per vedere prodotti simili o scegli di ricevere una notifica se torna disponibile."><div data-testid="002-out-of-stock">&nbsp;&nbsp;XS&nbsp;&nbsp;</div></div></li>
<li><div id="sizeButton-1" data-testid="sizeButton-1" role="radio" aria-label="Taglia M."><div data-testid="004-in-stock">&nbsp;&nbsp;M&nbsp;&nbsp;</div></div></li>
</ul></div>`)

	status, method, err := DetectStockStatus(rule, body)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status != "out_of_stock" || method != "hm_aria_label" {
		t.Fatalf("expected out_of_stock/hm_aria_label, got %s/%s", status, method)
	}
}

func TestDetectStockStatusHMSizeInStock(t *testing.T) {
	rule := []byte(`{"type":"hm_size","size":"M"}`)
	body := []byte(`<div data-testid="size-selector"><ul data-testid="grid">
<li><div id="sizeButton-0" data-testid="sizeButton-0" role="radio" aria-label="Taglia XS: esaurita."><div data-testid="002-out-of-stock">&nbsp;&nbsp;XS&nbsp;&nbsp;</div></div></li>
<li><div id="sizeButton-1" data-testid="sizeButton-1" role="radio" aria-label="Taglia M."><div data-testid="004-in-stock">&nbsp;&nbsp;M&nbsp;&nbsp;</div></div></li>
</ul></div>`)

	status, method, err := DetectStockStatus(rule, body)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status != "in_stock" || method != "hm_aria_label" {
		t.Fatalf("expected in_stock/hm_aria_label, got %s/%s", status, method)
	}
}

func TestDetectStockStatusHMSizeNoLongerListed(t *testing.T) {
	rule := []byte(`{"type":"hm_size","size":"XL"}`)
	body := []byte(`<div data-testid="size-selector"><ul data-testid="grid">
<li><div id="sizeButton-0" data-testid="sizeButton-0" role="radio" aria-label="Taglia M."><div data-testid="004-in-stock">&nbsp;&nbsp;M&nbsp;&nbsp;</div></div></li>
</ul></div>`)

	if _, _, err := DetectStockStatus(rule, body); err == nil {
		t.Fatal("expected an error when the tracked size is no longer listed")
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

func TestDetectStockStatusEtisalatSkuAPI(t *testing.T) {
	rule := []byte(`{"type":"etisalat_stock"}`)
	status, method, err := DetectStockStatus(rule, []byte(`{"skuId":"bgskuTrans4120911950","displayName":"PlayStation PS5 PRO standalone Console","inStock":false}`))
	if err != nil {
		t.Fatal(err)
	}
	if status != "out_of_stock" || method != "etisalat_sku_api" {
		t.Fatalf("got %s/%s", status, method)
	}

	// The eShop product page is an AngularJS shell with no stock wording; it must fail the
	// lookup instead of reading as in_stock.
	shell := []byte(`<!DOCTYPE html><html ng-app="app"><head><title>Get your favorite Gaming with smart payment plans by e&amp;.</title></head><body>` + strings.Repeat("<div ng-view></div>", 50) + `</body></html>`)
	if status, _, err := DetectStockStatus(rule, shell); err == nil {
		t.Fatalf("expected an error for the page shell, got %s", status)
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

func TestDetectStockStatusIgnoresInertVariationTemplate(t *testing.T) {
	// WooCommerce ships this template on every product page, in stock or not. Scanning it
	// reported an available item as out_of_stock — confirmed against supermaluch.com.
	body := []byte(`<html><body><div class="product">
<p class="price">5 896,00 zł</p>
<button class="single_add_to_cart_button">Dodaj do koszyka</button>
<script type="text/template" id="tmpl-unavailable-variation-template">
	<p role="alert">Przepraszamy, ten produkt jest niedostępny. Prosimy wybrać inną kombinację.</p>
</script>
<template id="tmpl-wvs-unavailable"><p>Sorry, this product is unavailable.</p></template>
<div class="description">Wózek modułowy Anex Modu w konfiguracji dwie gondole i dwie
spacerówki to wszechstronny zestaw dla rodziców, którzy potrzebują pełnej elastyczności
podczas codziennych spacerów. Zestaw oferuje możliwość korzystania z dwóch różnych
modułów naprzemiennie, w zależności od aktualnych potrzeb dziecka lub dzieci. Wysyłka
w ciągu 24 godzin, darmowa dostawa oraz dwuletnia gwarancja producenta.</div>
</div></body></html>`)

	if got := DetectStockStatusFromText(body); got != "in_stock" {
		t.Fatalf("status = %q, want in_stock (inert templates must not count)", got)
	}
}

func TestDetectStockStatusStillReadsRealOutOfStockWording(t *testing.T) {
	// The strip must not swallow a genuine signal that lives in ordinary page markup.
	body := []byte(`<html><body><div class="product">
<p class="stock out-of-stock">Brak w magazynie</p>
<script type="text/template" id="tmpl-unavailable-variation-template"><p>unavailable</p></script>
</div></body></html>`)

	if got := DetectStockStatusFromText(body); got != "out_of_stock" {
		t.Fatalf("status = %q, want out_of_stock", got)
	}
}

func TestDetectStockStatusKeepsOrdinaryScriptState(t *testing.T) {
	// Plain <script> bodies are left intact on purpose: on SPA-ish storefronts the embedded
	// JSON state is sometimes the only place availability appears at all.
	body := []byte(`<html><body><script>window.__STATE__={"availability":"out of stock"}</script></body></html>`)

	if got := DetectStockStatusFromText(body); got != "out_of_stock" {
		t.Fatalf("status = %q, want out_of_stock", got)
	}
}
