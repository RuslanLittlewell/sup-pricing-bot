package shops

import (
	"encoding/json"
	"fmt"
	"math"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"golang.org/x/net/html"
)

const RossmannPriceMethod = "rossmann_product_price"

var rossmannPricePattern = regexp.MustCompile(`\d[\d\s\x{00A0}]*(?:[,.]\d+)?`)

type RossmannPrice struct {
	Current  float64
	Currency string
}

func IsRossmannURL(rawURL string) bool {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	host := strings.ToLower(parsed.Hostname())
	return host == "rossmann.pl" || strings.HasSuffix(host, ".rossmann.pl")
}

// ParseRossmannPrice reads the main PDP price only from data-testid="product-price"
// and verifies it against the Product JSON-LD offer. Values elsewhere on the page (for
// example a 9.99 PLN shipping fee) must never be treated as a product price.
func ParseRossmannPrice(body []byte) (RossmannPrice, error) {
	doc, err := html.Parse(strings.NewReader(string(body)))
	if err != nil {
		return RossmannPrice{}, fmt.Errorf("parse html: %w", err)
	}

	domPrice, domCurrency, ok := rossmannDOMPrice(doc)
	if !ok {
		return RossmannPrice{}, fmt.Errorf(`Rossmann data-testid="product-price" not found`)
	}
	jsonLDPrice, jsonLDCurrency, ok := rossmannJSONLDPrice(doc)
	if !ok {
		return RossmannPrice{}, fmt.Errorf("Rossmann Product JSON-LD price not found")
	}
	if math.Round(domPrice*100) != math.Round(jsonLDPrice*100) {
		return RossmannPrice{}, fmt.Errorf("Rossmann price mismatch: product-price %.2f, JSON-LD %.2f", domPrice, jsonLDPrice)
	}

	currency := jsonLDCurrency
	if currency == "" {
		currency = domCurrency
	}
	return RossmannPrice{Current: domPrice, Currency: currency}, nil
}

func rossmannDOMPrice(doc *html.Node) (float64, string, bool) {
	var text string
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if text != "" {
			return
		}
		if n.Type == html.ElementNode && getNodeAttr(n, "data-testid") == "product-price" {
			text = nodeText(n)
			return
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	price, ok := parseRossmannPriceText(text)
	currency := ""
	if strings.Contains(strings.ToLower(text), "zł") {
		currency = "PLN"
	}
	return price, currency, ok
}

func rossmannJSONLDPrice(doc *html.Node) (float64, string, bool) {
	var price float64
	var currency string
	var found bool
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if found {
			return
		}
		if n.Type == html.ElementNode && n.Data == "script" && strings.EqualFold(getNodeAttr(n, "type"), "application/ld+json") && n.FirstChild != nil {
			var value any
			if json.Unmarshal([]byte(n.FirstChild.Data), &value) == nil {
				price, currency, found = findRossmannProductPrice(value)
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	return price, currency, found
}

func findRossmannProductPrice(value any) (float64, string, bool) {
	switch current := value.(type) {
	case []any:
		for _, item := range current {
			if price, currency, ok := findRossmannProductPrice(item); ok {
				return price, currency, true
			}
		}
	case map[string]any:
		if jsonLDTypeContains(current["@type"], "Product") {
			if offer, ok := current["offers"].(map[string]any); ok {
				if price, ok := jsonNumber(offer["price"]); ok && price > 0 {
					currency, _ := offer["priceCurrency"].(string)
					return price, currency, true
				}
			}
		}
		for _, child := range current {
			if price, currency, ok := findRossmannProductPrice(child); ok {
				return price, currency, true
			}
		}
	}
	return 0, "", false
}

func jsonLDTypeContains(value any, target string) bool {
	switch current := value.(type) {
	case string:
		return strings.EqualFold(current, target)
	case []any:
		for _, item := range current {
			if jsonLDTypeContains(item, target) {
				return true
			}
		}
	}
	return false
}

func jsonNumber(value any) (float64, bool) {
	switch current := value.(type) {
	case float64:
		return current, true
	case string:
		price, err := strconv.ParseFloat(strings.ReplaceAll(current, ",", "."), 64)
		return price, err == nil
	}
	return 0, false
}

func parseRossmannPriceText(text string) (float64, bool) {
	raw := rossmannPricePattern.FindString(text)
	if raw == "" {
		return 0, false
	}
	raw = strings.NewReplacer(" ", "", "\u00a0", "", ",", ".").Replace(raw)
	price, err := strconv.ParseFloat(raw, 64)
	return price, err == nil && price > 0
}

func getNodeAttr(n *html.Node, key string) string {
	for _, attr := range n.Attr {
		if attr.Key == key {
			return attr.Val
		}
	}
	return ""
}
