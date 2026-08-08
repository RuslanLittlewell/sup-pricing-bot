package extractor

import "testing"

func TestUsableBodyRejectsHMSoftBlock(t *testing.T) {
	body := []byte(`<html><head><title>Access Denied</title></head><body>
You don't have permission to access this page. Reference #18.abc
</body></html>`)

	if usableBody("https://www2.hm.com/it_it/productpage.1341273001.html", body) {
		t.Fatal("H&M soft-block page must not stop the fallback chain")
	}
}

func TestUsableBodyAcceptsHMProductPage(t *testing.T) {
	body := []byte(`<div data-testid="size-selector"><ul data-testid="grid">
<li><div data-testid="sizeButton-0" aria-label="Taglia XS: esaurita."><div>XS</div></div></li>
</ul></div>`)

	if !usableBody("https://www2.hm.com/it_it/productpage.1341273001.html", body) {
		t.Fatal("real H&M product page must be accepted")
	}
}

func TestUsableBodyKeepsGenericBehaviour(t *testing.T) {
	if !usableBody("https://example.com/product", []byte("any response body")) {
		t.Fatal("generic sites must keep accepting non-challenge bodies")
	}
}
