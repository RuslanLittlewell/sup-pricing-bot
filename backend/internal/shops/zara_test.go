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
