package extractor

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/littlewell/price-tracker/internal/shops"
)

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
	"powiadom mnie",
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

// minStockBodyBytes is the smallest body we'll treat as a real product page for the
// keyword scan. Below this, an empty/truncated/error response can't be distinguished from
// a genuine in-stock page (which has no positive marker of its own), and defaulting such a
// body to "in_stock" is exactly what produced spurious "back in stock" notifications. It's
// deliberately low — only degenerate responses fall under it; sophisticated bot-challenge
// or error pages that are large enough are the fetcher's isBotChallenge job, not this one.
const minStockBodyBytes = 512

// DetectStockStatusFromText does a plain case-insensitive search for known "out of stock"
// phrases on the raw page body. Used by the availability-only tracking mode, which doesn't
// try to parse a price. Returns "out_of_stock" on a phrase match, "unknown" when the body
// is too small to be a real product page (see minStockBodyBytes), and "in_stock"
// otherwise — since the absence of an out-of-stock phrase on a substantive page is our only
// available in-stock signal.
func DetectStockStatusFromText(htmlContent []byte) string {
	lowered := strings.ToLower(string(htmlContent))
	for _, phrase := range outOfStockPhrases {
		if strings.Contains(lowered, phrase) {
			return "out_of_stock"
		}
	}
	if len(strings.TrimSpace(lowered)) < minStockBodyBytes {
		return "unknown"
	}
	return "in_stock"
}

// StockRule is a stock tracker's extraction_rule — set for trackers that watch one
// specific variant (currently only "zara_size") rather than the item as a whole.
type StockRule struct {
	Type    string `json:"type"`
	Size    string `json:"size"`
	SKU     string `json:"sku"`
	GTIN    string `json:"gtin"`
	Article string `json:"article"`
}

// DetectStockStatus resolves in_stock/out_of_stock for a stock tracker and reports which
// method actually parsed the page — "keyword_scan" for the generic whole-page phrase
// match, "json_ld" for a "zara_size" tracker (shops.ParseZaraSizes reads the same
// schema.org JSON-LD structured data price trackers already report as "json_ld", just
// picking one size's offer out of it instead of the top-level one). "zara_size" itself is
// the tracker's *rule type* (which variant to watch), not the parsing method — mirrors
// extraction_method on price_points so the admin dashboard can show it the same way for
// stock trackers. Shared by the worker's periodic recheck and the bot's manual /check.
func DetectStockStatus(extractionRuleJSON []byte, body []byte) (status, method string, err error) {
	if len(extractionRuleJSON) > 0 && string(extractionRuleJSON) != "{}" {
		var rule StockRule
		if jsonErr := json.Unmarshal(extractionRuleJSON, &rule); jsonErr == nil && rule.Type == "zara_size" {
			variants, zerr := shops.ParseZaraSizes(body)
			if zerr != nil {
				return "", "", fmt.Errorf("zara size lookup failed: %w", zerr)
			}
			for _, v := range variants {
				if v.Size == rule.Size {
					if v.InStock {
						return "in_stock", "json_ld", nil
					}
					return "out_of_stock", "json_ld", nil
				}
			}
			return "", "", fmt.Errorf("size %q is no longer listed on the page", rule.Size)
		}
		if jsonErr := json.Unmarshal(extractionRuleJSON, &rule); jsonErr == nil && rule.Type == "wildberries_stock" {
			product, werr := shops.ParseWildberriesProduct(body, rule.Article)
			if werr != nil {
				return "", "", fmt.Errorf("wildberries stock lookup failed: %w", werr)
			}
			if product.InStock {
				return "in_stock", "wildberries_api", nil
			}
			return "out_of_stock", "wildberries_api", nil
		}
		if jsonErr := json.Unmarshal(extractionRuleJSON, &rule); jsonErr == nil && rule.Type == "nike_size" {
			available, nikeErr := shops.ParseNikeGTINAvailability(body, rule.GTIN)
			if nikeErr != nil {
				return "", "", fmt.Errorf("Nike size lookup failed: %w", nikeErr)
			}
			if available {
				return "in_stock", shops.NikeStockMethod, nil
			}
			return "out_of_stock", shops.NikeStockMethod, nil
		}
	}
	status = DetectStockStatusFromText(body)
	if status == "unknown" {
		// A body too small/degenerate to read is an extraction failure, not a real stock
		// signal — returning an error routes it through the worker's error handling (retry,
		// then needs_confirmation) instead of recording a bogus status change and firing a
		// false "back in stock"/"out of stock" notification off it.
		return "", "", fmt.Errorf("page too small to determine stock status (%d bytes)", len(body))
	}
	return status, "keyword_scan", nil
}
