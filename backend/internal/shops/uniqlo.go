package shops

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"net/url"
	"strings"
)

const UniqloPriceMethod = "uniqlo_product_data"

type UniqloPrice struct {
	Current         float64
	Regular         float64
	Currency        string
	DiscountPercent int
}

func IsUniqloURL(rawURL string) bool {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	host := strings.ToLower(parsed.Hostname())
	return host == "uniqlo.com" || strings.HasSuffix(host, ".uniqlo.com")
}

func uniqloProductID(rawURL string) string {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	for _, part := range strings.Split(strings.Trim(parsed.Path, "/"), "/") {
		if strings.HasPrefix(part, "E") && strings.Contains(part, "-") {
			return strings.TrimSuffix(part, "-00")
		}
	}
	return ""
}

// ParseUniqloPrice reads the product-specific price from window.__PRELOADED_STATE__.
// Looking up productId prevents prices from recommendations and global UI copy from
// being mistaken for the PDP price.
func ParseUniqloPrice(body []byte, rawURL string) (UniqloPrice, error) {
	productID := uniqloProductID(rawURL)
	if productID == "" {
		return UniqloPrice{}, fmt.Errorf("Uniqlo product id not found in URL")
	}
	marker := []byte("window.__PRELOADED_STATE__ =")
	start := bytes.Index(body, marker)
	if start < 0 {
		return UniqloPrice{}, fmt.Errorf("Uniqlo preloaded state not found")
	}
	var state any
	if err := json.NewDecoder(bytes.NewReader(body[start+len(marker):])).Decode(&state); err != nil {
		return UniqloPrice{}, fmt.Errorf("decode Uniqlo preloaded state: %w", err)
	}
	price := findUniqloPrice(state, productID)
	if price.Current <= 0 {
		return UniqloPrice{}, fmt.Errorf("no Uniqlo price found for %s", productID)
	}
	if price.Regular > price.Current {
		price.DiscountPercent = int(math.Round((price.Regular - price.Current) / price.Regular * 100))
	}
	return price, nil
}

func findUniqloPrice(value any, productID string) UniqloPrice {
	switch current := value.(type) {
	case []any:
		for _, child := range current {
			if result := findUniqloPrice(child, productID); result.Current > 0 {
				return result
			}
		}
	case map[string]any:
		id, _ := current["productId"].(string)
		if id == productID {
			if prices, ok := current["prices"].(map[string]any); ok {
				base, baseOK := uniqloPriceValue(prices["base"])
				promo, promoOK := uniqloPriceValue(prices["promo"])
				currency := uniqloCurrency(prices["base"])
				if promoOK && promo > 0 {
					return UniqloPrice{Current: promo, Regular: base, Currency: currency}
				}
				if baseOK && base > 0 {
					return UniqloPrice{Current: base, Regular: base, Currency: currency}
				}
			}
		}
		for _, child := range current {
			if result := findUniqloPrice(child, productID); result.Current > 0 {
				return result
			}
		}
	}
	return UniqloPrice{}
}

func uniqloPriceValue(value any) (float64, bool) {
	price, ok := value.(map[string]any)
	if !ok {
		return 0, false
	}
	return jsonNumber(price["value"])
}

func uniqloCurrency(value any) string {
	price, _ := value.(map[string]any)
	currency, _ := price["currency"].(map[string]any)
	code, _ := currency["code"].(string)
	return code
}
