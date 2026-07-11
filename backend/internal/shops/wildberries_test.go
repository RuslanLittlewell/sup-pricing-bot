package shops

import "testing"

func TestParseWildberriesProductIncludesLogistics(t *testing.T) {
	body := []byte(`{"products":[{"id":781465705,"brand":"Nike","name":"Кроссовки","sizes":[{"stocks":[{"qty":3}],"price":{"product":10884,"logistics":1160}},{"stocks":[],"price":{"product":9000,"logistics":1000}}]}]}`)
	product, err := ParseWildberriesProduct(body, "781465705")
	if err != nil {
		t.Fatal(err)
	}
	if product.Price != 120.44 {
		t.Fatalf("price = %.2f, want 120.44", product.Price)
	}
	if !product.InStock {
		t.Fatal("expected in stock")
	}
}

func TestWildberriesAPIURLBelarus(t *testing.T) {
	got, err := WildberriesAPIURL("https://www.wildberries.by/catalog/781465705/detail.aspx")
	if err != nil {
		t.Fatal(err)
	}
	if got != "https://card.wb.ru/cards/v4/detail?appType=1&curr=byn&dest=-59202&locale=ru&nm=781465705" {
		t.Fatalf("unexpected API URL: %s", got)
	}
}
