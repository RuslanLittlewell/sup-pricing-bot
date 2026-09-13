package shops

import "testing"

const etisalatPS5ProURL = "https://prodnew.etisalat.ae/b2c/eshop/device-configuration/key-prod4t1777373426348?productId=prod4T1777373426348&skuId=bgskuTrans4120911950&catName=Gaming&listVal=GamingConsoles&showDefault=true"

func TestIsEtisalatURL(t *testing.T) {
	for rawURL, want := range map[string]bool{
		etisalatPS5ProURL:                         true,
		"https://www.etisalat.ae/b2c/eshop/x":     true,
		"https://etisalat.ae/b2c/eshop/x":         true,
		"https://notetisalat.ae/b2c/eshop/x":      false,
		"https://etisalat.ae.example.com/b2c/x":   false,
		"https://www.amazon.ae/dp/B0BXGFFSL1?x=1": false,
	} {
		if got := IsEtisalatURL(rawURL); got != want {
			t.Errorf("IsEtisalatURL(%q) = %v, want %v", rawURL, got, want)
		}
	}
}

func TestEtisalatSkuAPIURL(t *testing.T) {
	apiURL, err := EtisalatSkuAPIURL(etisalatPS5ProURL)
	if err != nil {
		t.Fatal(err)
	}
	if want := "https://prodnew.etisalat.ae/b2c/eshop/getDeviceSku/bgskuTrans4120911950"; apiURL != want {
		t.Fatalf("expected %s, got %s", want, apiURL)
	}

	if _, err := EtisalatSkuAPIURL("https://prodnew.etisalat.ae/b2c/eshop/device-configuration/key-prod4t1777373426348?productId=prod4T1777373426348"); err == nil {
		t.Fatal("expected an error for a URL without skuId")
	}
	if _, err := EtisalatSkuAPIURL("https://www.amazon.ae/dp/B0BXGFFSL1?skuId=1"); err == nil {
		t.Fatal("expected an error for a non-Etisalat URL")
	}
}

// The product page is an AngularJS shell with no stock data; the storefront reads it from
// this SKU endpoint (trimmed real response for a PS5 Pro shown as OUT OF STOCK).
func TestParseEtisalatSku(t *testing.T) {
	body := []byte(`{"skuId":"bgskuTrans4120911950","displayName":"PlayStation PS5 PRO standalone Console","listPrice":"3500.0","listPriceIncVat":"3500.0","oldPrice":"","inStock":false,"isInStockContract":false,"isInStockNonContract":false}`)
	sku, err := ParseEtisalatSku(body)
	if err != nil {
		t.Fatal(err)
	}
	if sku.InStock || sku.DisplayName != "PlayStation PS5 PRO standalone Console" {
		t.Fatalf("expected out-of-stock PS5 Pro, got %+v", sku)
	}

	sku, err = ParseEtisalatSku([]byte(`{"skuId":"bgsku1","displayName":"Console","inStock":true}`))
	if err != nil || !sku.InStock {
		t.Fatalf("expected in-stock SKU, got %+v, err=%v", sku, err)
	}
}

func TestParseEtisalatSkuRejectsNonSkuBodies(t *testing.T) {
	for _, body := range []string{
		`<!DOCTYPE html><html ng-app="app"><title>Get your favorite Gaming</title></html>`,
		`{"errorCode":"404","message":"SKU not found"}`,
		`{"skuId":"bgsku1","displayName":"Console"}`,
	} {
		if sku, err := ParseEtisalatSku([]byte(body)); err == nil {
			t.Errorf("expected an error for %q, got %+v", body, sku)
		}
	}
}
