package extractor

import (
	"encoding/json"
	"fmt"
	"regexp"
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
	// Italiano
	"esaurito",
	"esaurita",
	"esauriti",
	"esaurite",
	"non disponibile",
	"temporaneamente esaurito",
	"temporaneamente non disponibile",
	"attualmente non disponibile",
	"avvisami",
	"prodotto non disponibile",
	"ritirato dalla vendita",
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
	if ContainsOutOfStockPhrase(string(stripInertMarkup(htmlContent))) {
		return "out_of_stock"
	}
	// Deliberately measured on the original body: the point of this floor is to catch a
	// truncated or empty fetch, and stripping inert markup out of a real page could
	// otherwise push it under the threshold and report "unknown" for a page we did read.
	if len(strings.TrimSpace(string(htmlContent))) < minStockBodyBytes {
		return "unknown"
	}
	return "in_stock"
}

// inertMarkupRe matches script/template blocks that hold unrendered client-side templates
// rather than page content. Their bodies are boilerplate that ships on every page of a
// store regardless of its actual stock: WooCommerce, for one, embeds
// <script type="text/template" id="tmpl-unavailable-variation-template"> containing
// "Sorry, this product is unavailable" on every product page it renders, which the phrase
// scan otherwise reads as a positive out-of-stock signal for an in-stock item.
//
// Only types the browser never renders as-is are stripped. Ordinary <script> bodies are
// left alone on purpose — on SPA-ish storefronts the embedded JSON state is sometimes the
// only place a genuine stock signal appears at all.
var inertMarkupRe = regexp.MustCompile(`(?is)<script[^>]*\stype\s*=\s*["'](?:text/template|text/x-template|text/html)["'][^>]*>.*?</script>|<template\b[^>]*>.*?</template>`)

func stripInertMarkup(htmlContent []byte) []byte {
	return inertMarkupRe.ReplaceAll(htmlContent, []byte(" "))
}

// ContainsOutOfStockPhrase does the same case-insensitive phrase match as
// DetectStockStatusFromText, without that function's minimum-body-size gate — which exists
// to distinguish a genuinely empty in-stock page from a truncated/broken fetch, a concern
// that doesn't apply to a short, deliberately-scoped snippet like one size button's
// aria-label. Shared with per-variant checks (e.g. shops.HMSizeVariant.Label) that want the
// same locale-aware phrase list without that gate misfiring on their inherently small input.
func ContainsOutOfStockPhrase(text string) bool {
	lowered := strings.ToLower(text)
	for _, phrase := range outOfStockPhrases {
		if strings.Contains(lowered, phrase) {
			return true
		}
	}
	return false
}

// StockRule is a stock tracker's extraction_rule — set for trackers that watch one
// specific variant ("zara_size", "hm_size", "nike_size", "wildberries_stock") rather than
// the item as a whole.
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
// A "zara_size" tracker can also report "keyword_scan": when the product is fully
// sold out, Zara omits the hasVariant JSON-LD block entirely (no size picker to render),
// so we fall back to the same phrase scan the generic path uses. "hm_size" reports
// shops.HMStockMethod — H&M has no structured variant data at all, so per-size status there
// always comes from running the same out-of-stock phrase list against one size button's
// aria-label instead (see shops.ParseHMSizes and ContainsOutOfStockPhrase).
func DetectStockStatus(extractionRuleJSON []byte, body []byte) (status, method string, err error) {
	if len(extractionRuleJSON) > 0 && string(extractionRuleJSON) != "{}" {
		var rule StockRule
		if jsonErr := json.Unmarshal(extractionRuleJSON, &rule); jsonErr == nil && rule.Type == "zara_size" {
			variants, zerr := shops.ParseZaraSizes(body)
			if zerr != nil {
				// A fully sold-out Zara product renders no size picker at all: there's
				// nothing to pick from, so the ProductGroup/hasVariant JSON-LD block
				// ParseZaraSizes looks for is simply absent, and the page shows a
				// disabled button (zds-button__second-line) reading "out of stock" in
				// the page's locale instead. That's a confident, positive stock signal —
				// not a parse failure — so check the generic out-of-stock phrase list
				// (outOfStockPhrases already covers Polish/Russian/English wording, e.g.
				// "нет в наличии") before giving up. Only fall through to the error when
				// the page doesn't confirm out-of-stock either: defaulting an unreadable
				// page to "in_stock" is exactly what produced spurious "back in stock"
				// notifications elsewhere (see DetectStockStatusFromText).
				if DetectStockStatusFromText(body) == "out_of_stock" {
					return "out_of_stock", "keyword_scan", nil
				}
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
		if jsonErr := json.Unmarshal(extractionRuleJSON, &rule); jsonErr == nil && rule.Type == "hm_size" {
			variants, herr := shops.ParseHMSizes(body)
			if herr != nil {
				return "", "", fmt.Errorf("hm size lookup failed: %w", herr)
			}
			for _, v := range variants {
				if v.Size == rule.Size {
					if ContainsOutOfStockPhrase(v.Label) {
						return "out_of_stock", shops.HMStockMethod, nil
					}
					return "in_stock", shops.HMStockMethod, nil
				}
			}
			return "", "", fmt.Errorf("size %q is no longer listed on the page", rule.Size)
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
