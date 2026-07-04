// Package shops holds site-specific extraction logic — helpers that read structured
// data particular to one retailer's markup, rather than the generic heuristics in
// internal/extractor. Add one file per site as new per-site quirks come up.
package shops

import (
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"golang.org/x/net/html"
)

// IsZaraURL reports whether rawURL points at zara.com (any locale). Zara embeds a
// schema.org ProductGroup/hasVariant JSON-LD block with live per-size availability on
// every product page, so unlike most sites we don't need to click "add to cart" (which
// only opens a picker backed by the same data) to know which sizes are in stock.
func IsZaraURL(rawURL string) bool {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	host := strings.ToLower(parsed.Hostname())
	return host == "zara.com" || strings.HasSuffix(host, ".zara.com")
}

// ZaraSizeVariant is one size option of a Zara product, with its live availability.
type ZaraSizeVariant struct {
	Size    string `json:"size"`
	SKU     string `json:"sku"`
	InStock bool   `json:"in_stock"`
}

// ParseZaraSizes reads the schema.org ProductGroup/hasVariant JSON-LD block off a
// (rendered) Zara product page and returns one entry per size.
func ParseZaraSizes(htmlContent []byte) ([]ZaraSizeVariant, error) {
	doc, err := html.Parse(strings.NewReader(string(htmlContent)))
	if err != nil {
		return nil, fmt.Errorf("parse html: %w", err)
	}

	var variants []ZaraSizeVariant
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if variants != nil {
			return
		}
		if n.Type == html.ElementNode && n.Data == "script" {
			isLD := false
			for _, attr := range n.Attr {
				if attr.Key == "type" && attr.Val == "application/ld+json" {
					isLD = true
					break
				}
			}
			if isLD && n.FirstChild != nil {
				variants = parseZaraProductGroup(n.FirstChild.Data)
			}
		}
		for c := n.FirstChild; c != nil && variants == nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)

	if len(variants) == 0 {
		return nil, fmt.Errorf("no size data found on page")
	}
	return variants, nil
}

// zaraPriceAttrRe matches Zara's <data data-currency="PLN" value="299.00"> price nodes.
var zaraPriceAttrRe = regexp.MustCompile(`data-currency="([A-Za-z]{3})"\s+value="([0-9]+(?:\.[0-9]+)?)"`)

// ParseZaraPrice returns the product's actual current price off a (rendered) Zara PDP.
// EU price-transparency rules ("Omnibus Directive") require Zara to show, alongside a
// discounted item's current price, its pre-discount reference price(s) — a marked-down
// page carries several `<data data-currency="..." value="...">` nodes (original price,
// 30-day-low, current price), and the reference prices are by definition never lower than
// what the item actually sells for now. So rather than trust whichever price node a
// screenshot/text search happens to land on (which can lock onto a crossed-out reference
// price — see the price_token_index=0 bug this replaces), take the smallest value found;
// on a non-discounted page there's only one node and this is just that price.
func ParseZaraPrice(body []byte) (price, currency string, ok bool) {
	matches := zaraPriceAttrRe.FindAllStringSubmatch(string(body), -1)
	lowest := 0.0
	for _, m := range matches {
		val, err := strconv.ParseFloat(m[2], 64)
		if err != nil {
			continue
		}
		if !ok || val < lowest {
			ok = true
			lowest = val
			currency = m[1]
			price = m[2]
		}
	}
	return price, currency, ok
}

func parseZaraProductGroup(raw string) []ZaraSizeVariant {
	var obj map[string]interface{}
	if err := json.Unmarshal([]byte(raw), &obj); err != nil {
		return nil
	}
	typ, _ := obj["@type"].(string)
	if !strings.Contains(typ, "ProductGroup") {
		return nil
	}
	variantsRaw, ok := obj["hasVariant"].([]interface{})
	if !ok {
		return nil
	}

	var result []ZaraSizeVariant
	for _, v := range variantsRaw {
		vm, ok := v.(map[string]interface{})
		if !ok {
			continue
		}
		size, _ := vm["size"].(string)
		if size == "" {
			continue
		}
		sku, _ := vm["sku"].(string)
		inStock := false
		if offers, ok := vm["offers"].(map[string]interface{}); ok {
			avail, _ := offers["availability"].(string)
			inStock = strings.Contains(avail, "InStock")
		}
		result = append(result, ZaraSizeVariant{Size: size, SKU: sku, InStock: inStock})
	}
	return result
}
