package shops

import (
	"strings"
	"testing"
)

func TestIsHMURL(t *testing.T) {
	cases := map[string]bool{
		"https://www2.hm.com/it_it/productpage.1341273001.html": true,
		"https://www.hm.com/en_us/productpage.123.html":         true,
		"https://hm.com/foo":                                    true,
		"https://www.massimodutti.com/pl/p-123.html":            false,
		"not a url": false,
	}
	for url, want := range cases {
		if got := IsHMURL(url); got != want {
			t.Errorf("IsHMURL(%q) = %v, want %v", url, got, want)
		}
	}
}

func TestParseHMSizesAllSoldOut(t *testing.T) {
	// Trimmed from the actual rendered DOM of a fully sold-out it_it product page
	// (www2.hm.com/it_it/productpage.1341273001.html) — every size shows "esaurita" in its
	// aria-label and the button carries a "-out-of-stock" data-testid suffix.
	html := `<div data-testid="size-selector"><ul data-testid="grid">
<li><div id="sizeButton-0" data-testid="sizeButton-0" role="radio" aria-label="Taglia XS: esaurita. Seleziona per vedere prodotti simili o scegli di ricevere una notifica se torna disponibile."><div data-testid="002-out-of-stock">&nbsp;&nbsp;XS&nbsp;&nbsp;</div></div></li>
<li><div id="sizeButton-1" data-testid="sizeButton-1" role="radio" aria-label="Taglia S: esaurita. Seleziona per vedere prodotti simili o scegli di ricevere una notifica se torna disponibile."><div data-testid="003-out-of-stock">&nbsp;&nbsp;S&nbsp;&nbsp;</div></div></li>
</ul></div>`

	variants, err := ParseHMSizes([]byte(html))
	if err != nil {
		t.Fatalf("ParseHMSizes failed: %v", err)
	}
	if len(variants) != 2 {
		t.Fatalf("expected 2 variants, got %d: %+v", len(variants), variants)
	}
	if variants[0].Size != "XS" || !strings.Contains(variants[0].Label, "esaurita") {
		t.Errorf("expected XS label to carry the sold-out phrase, got %+v", variants[0])
	}
	if variants[1].Size != "S" || !strings.Contains(variants[1].Label, "esaurita") {
		t.Errorf("expected S label to carry the sold-out phrase, got %+v", variants[1])
	}
}

func TestParseHMSizesAvailable(t *testing.T) {
	html := `<div data-testid="size-selector"><ul data-testid="grid">
<li><div id="sizeButton-0" data-testid="sizeButton-0" role="radio" aria-label="Taglia M."><div data-testid="004-in-stock">&nbsp;&nbsp;M&nbsp;&nbsp;</div></div></li>
</ul></div>`

	variants, err := ParseHMSizes([]byte(html))
	if err != nil {
		t.Fatalf("ParseHMSizes failed: %v", err)
	}
	if len(variants) != 1 || variants[0].Size != "M" {
		t.Fatalf("unexpected HM variants: %+v", variants)
	}
	if strings.Contains(variants[0].Label, "esaurit") {
		t.Errorf("expected M's label to carry no sold-out phrase, got %q", variants[0].Label)
	}
}

func TestHasHMSizePickerRejectsAkamaiBlockPage(t *testing.T) {
	// Verbatim body www2.hm.com returns to the production server. It carries no
	// out-of-stock wording, so the generic keyword scan reads it as in_stock — the false
	// "in stock" this guard exists to stop.
	blocked := []byte(`<html><head>
<title>Access Denied</title>
</head><body>
<h1>Access Denied</h1>
You don't have permission to access "http://www2.hm.com/it_it/productpage.1341273001.html" on this server.<p>
Reference #18.bc1f1602.1786122111.32343e34
</p><p>https://errors.edgesuite.net/18.bc1f1602.1786122111.32343e34</p>
</body></html>`)

	if HasHMSizePicker(blocked) {
		t.Fatal("an Akamai block page must not pass as a reachable H&M product page")
	}
}

func TestHasHMSizePickerAcceptsRealProductPage(t *testing.T) {
	page := []byte(`<div data-testid="size-selector"><ul data-testid="grid">
<li><div id="sizeButton-0" data-testid="sizeButton-0" role="radio" aria-label="Taglia M."><div>&nbsp;&nbsp;M&nbsp;&nbsp;</div></div></li>
</ul></div>`)
	if !HasHMSizePicker(page) {
		t.Fatal("a real H&M product page with a size picker must pass")
	}
}

func TestParseHMSizesNoSizeSelector(t *testing.T) {
	if _, err := ParseHMSizes([]byte("<html><body>no size picker here</body></html>")); err == nil {
		t.Fatal("expected an error when no size buttons are present")
	}
}
