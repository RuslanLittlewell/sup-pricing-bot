package extractor

import "strings"

// outOfStockPhrases lists common "not available" phrases across the languages we support.
// Any case-insensitive match on the raw page body marks a tracker as out of stock.
var outOfStockPhrases = []string{
	// English
	"out of stock",
	"sold out",
	"not in stock",
	"currently unavailable",
	"temporarily out of stock",
	"unavailable",
	"no longer available",
	"discontinued",
	"back in stock soon",
	"back soon",
	"coming soon",
	"check availability",
	"available on backorder",
	"available for pre-order",
	"unavailable from supplier",
	// Polski
	"brak w magazynie",
	"brak na stanie",
	"produkt wyprzedany",
	"wyprzedane",
	"chwilowo niedostępny",
	"niedostępny",
	"oczekujemy dostawy",
	"wkrótce dostępny",
	"zapytaj o dostępność",
	"brak u dostawcy",
	"produkt wycofany ze sprzedaży",
	// Русский
	"нет в наличии",
	"нет на складе",
	"товар распродан",
	"распродано",
	"временно нет в наличии",
	"временно отсутствует",
	"недоступно",
	"ожидается поступление",
	"скоро в наличии",
	"уточнить наличие",
	"нет у поставщика",
	"снят с продажи",
	"больше не продаётся",
}

// DetectStockStatusFromText does a plain case-insensitive search for known "out of stock"
// phrases on the raw page body. Used by the availability-only tracking mode, which doesn't
// try to parse a price.
func DetectStockStatusFromText(htmlContent []byte) string {
	lowered := strings.ToLower(string(htmlContent))
	for _, phrase := range outOfStockPhrases {
		if strings.Contains(lowered, phrase) {
			return "out_of_stock"
		}
	}
	return "in_stock"
}
