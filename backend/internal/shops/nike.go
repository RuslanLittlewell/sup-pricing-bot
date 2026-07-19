package shops

import (
	"encoding/json"
	"fmt"
	"math"
	"net/url"
	"strings"

	"golang.org/x/net/html"
)

const NikeStockMethod = "nike_inventory_api"
const NikePriceMethod = "nike_product_data"

type NikePrice struct {
	Current         float64
	Initial         float64
	Currency        string
	DiscountPercent int
}

type NikeSizeVariant struct {
	Size    string `json:"size"`
	SKU     string `json:"sku"`
	GTIN    string `json:"gtin"`
	InStock bool   `json:"in_stock"`
}

func IsNikeURL(rawURL string) bool {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	host := strings.ToLower(parsed.Hostname())
	return host == "nike.com" || strings.HasSuffix(host, ".nike.com")
}

func NikeStyleColor(rawURL string) string {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	parts := strings.Split(strings.Trim(parsed.Path, "/"), "/")
	if len(parts) == 0 {
		return ""
	}
	return strings.ToUpper(parts[len(parts)-1])
}

func NikeAvailabilityAPIURL(rawURL string) (string, error) {
	styleColor := NikeStyleColor(rawURL)
	if styleColor == "" {
		return "", fmt.Errorf("Nike styleColor not found in URL")
	}
	// Nike rejects the otherwise valid percent-encoded parentheses produced by
	// url.Values (styleColor%28...%29) with HTTP 400. styleColor comes from one
	// URL path segment; PathEscape prevents it from injecting query syntax while
	// preserving the literal filter parentheses required by this endpoint.
	return "https://api.nike.com/deliver/available_gtins/v3?filter=styleColor(" + url.PathEscape(styleColor) + ")&filter=merchGroup(EU)", nil
}

// ParseNikePrice reads the selected styleColor's product data embedded in the PDP.
// This avoids broad price scans that can pick another colourway, a recommendation,
// or a non-product monetary value elsewhere on the page.
func ParseNikePrice(body []byte, styleColor string) (NikePrice, error) {
	doc, err := html.Parse(strings.NewReader(string(body)))
	if err != nil {
		return NikePrice{}, fmt.Errorf("parse Nike HTML: %w", err)
	}
	var result NikePrice
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if result.Current > 0 {
			return
		}
		if n.Type == html.ElementNode && n.Data == "script" && n.FirstChild != nil {
			var value any
			if json.Unmarshal([]byte(n.FirstChild.Data), &value) == nil {
				result = findNikePrice(value, styleColor)
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	if result.Current <= 0 {
		return NikePrice{}, fmt.Errorf("no Nike price found for %s", styleColor)
	}
	if result.Initial > result.Current {
		result.DiscountPercent = int(math.Round((result.Initial - result.Current) / result.Initial * 100))
	}
	return result, nil
}

func findNikePrice(value any, styleColor string) NikePrice {
	switch current := value.(type) {
	case []any:
		for _, child := range current {
			if result := findNikePrice(child, styleColor); result.Current > 0 {
				return result
			}
		}
	case map[string]any:
		currentStyle, _ := current["styleColor"].(string)
		if strings.EqualFold(currentStyle, styleColor) {
			if prices, ok := current["prices"].(map[string]any); ok {
				currentPrice, currentOK := jsonNumber(prices["currentPrice"])
				initialPrice, _ := jsonNumber(prices["initialPrice"])
				currency, _ := prices["currency"].(string)
				if currentOK && currentPrice > 0 {
					return NikePrice{Current: currentPrice, Initial: initialPrice, Currency: currency}
				}
			}
		}
		for _, child := range current {
			if result := findNikePrice(child, styleColor); result.Current > 0 {
				return result
			}
		}
	}
	return NikePrice{}
}

// ParseNikeSizes joins the size/GTIN catalogue embedded in the PDP with Nike's live
// available_gtins inventory response. A catalogue size with status ACTIVE is not proof
// of stock; only the inventory API's available flag is used for InStock.
func ParseNikeSizes(pageBody, inventoryBody []byte, styleColor string) ([]NikeSizeVariant, error) {
	catalogue, err := parseNikeSizeCatalogue(pageBody, styleColor)
	if err != nil {
		return nil, err
	}
	availability, err := parseNikeAvailability(inventoryBody)
	if err != nil {
		return nil, err
	}
	for i := range catalogue {
		catalogue[i].InStock = availability[catalogue[i].GTIN]
	}
	return catalogue, nil
}

func ParseNikeGTINAvailability(inventoryBody []byte, gtin string) (bool, error) {
	availability, err := parseNikeAvailability(inventoryBody)
	if err != nil {
		return false, err
	}
	available, ok := availability[gtin]
	if !ok {
		return false, fmt.Errorf("Nike GTIN %q not found in inventory", gtin)
	}
	return available, nil
}

func parseNikeSizeCatalogue(body []byte, styleColor string) ([]NikeSizeVariant, error) {
	doc, err := html.Parse(strings.NewReader(string(body)))
	if err != nil {
		return nil, fmt.Errorf("parse Nike HTML: %w", err)
	}
	var variants []NikeSizeVariant
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if len(variants) > 0 {
			return
		}
		if n.Type == html.ElementNode && n.Data == "script" && n.FirstChild != nil {
			var value any
			if json.Unmarshal([]byte(n.FirstChild.Data), &value) == nil {
				variants = findNikeSizeCatalogue(value, styleColor)
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	if len(variants) == 0 {
		return nil, fmt.Errorf("no Nike size catalogue found for %s", styleColor)
	}
	return variants, nil
}

func findNikeSizeCatalogue(value any, styleColor string) []NikeSizeVariant {
	switch current := value.(type) {
	case []any:
		for _, child := range current {
			if variants := findNikeSizeCatalogue(child, styleColor); len(variants) > 0 {
				return variants
			}
		}
	case map[string]any:
		currentStyle, _ := current["styleColor"].(string)
		if strings.EqualFold(currentStyle, styleColor) {
			if sizes, ok := current["sizes"].([]any); ok {
				variants := make([]NikeSizeVariant, 0, len(sizes))
				for _, rawSize := range sizes {
					size, ok := rawSize.(map[string]any)
					if !ok {
						continue
					}
					label, _ := size["localizedLabel"].(string)
					sku, _ := size["merchSkuId"].(string)
					gtin := firstNikeGTIN(size["gtins"])
					if label != "" && gtin != "" {
						variants = append(variants, NikeSizeVariant{Size: "EU " + label, SKU: sku, GTIN: gtin})
					}
				}
				if len(variants) > 0 {
					return variants
				}
			}
		}
		for _, child := range current {
			if variants := findNikeSizeCatalogue(child, styleColor); len(variants) > 0 {
				return variants
			}
		}
	}
	return nil
}

func firstNikeGTIN(value any) string {
	gtins, _ := value.([]any)
	for _, raw := range gtins {
		if item, ok := raw.(map[string]any); ok {
			if gtin, ok := item["gtin"].(string); ok {
				return gtin
			}
		}
	}
	return ""
}

func parseNikeAvailability(body []byte) (map[string]bool, error) {
	var payload struct {
		Objects []struct {
			GTIN      string `json:"gtin"`
			Method    string `json:"method"`
			Available bool   `json:"available"`
		} `json:"objects"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("parse Nike inventory: %w", err)
	}
	if len(payload.Objects) == 0 {
		return nil, fmt.Errorf("Nike inventory is empty")
	}
	result := make(map[string]bool, len(payload.Objects))
	for _, item := range payload.Objects {
		if item.GTIN == "" || (item.Method != "" && item.Method != "SHIP") {
			continue
		}
		result[item.GTIN] = item.Available
	}
	return result, nil
}
