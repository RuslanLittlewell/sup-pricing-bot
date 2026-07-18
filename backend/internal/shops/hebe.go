package shops

import (
	"encoding/json"
	"fmt"
	"math"
	"net/url"
	"strings"

	"golang.org/x/net/html"
)

const HebePriceMethod = "hebe_gtm"
const HebeStockMethod = "hebe_add_to_cart"

func IsHebeURL(rawURL string) bool {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	host := strings.ToLower(parsed.Hostname())
	return host == "hebe.pl" || strings.HasSuffix(host, ".hebe.pl")
}

type HebePrice struct {
	Current         float64
	Regular         float64
	DiscountPercent int
	Currency        string
}

// HasHebeAddToCartButton reports whether Hebe renders the active add-to-cart
// control for the main product. Hebe's page contains generic and hidden strings such
// as "Powiadom mnie" and "niedostępny" even when the product is available, so a
// whole-document keyword scan produces false out-of-stock results. The
// add-product-detail/js-add-to-cart-initial classes identify the product-detail
// control rather than buttons belonging to recommendations.
func HasHebeAddToCartButton(body []byte) bool {
	doc, err := html.Parse(strings.NewReader(string(body)))
	if err != nil {
		return false
	}

	var found bool
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if found {
			return
		}
		if n.Type == html.ElementNode && n.Data == "button" {
			var className, title, value string
			for _, attr := range n.Attr {
				switch attr.Key {
				case "class":
					className = attr.Val
				case "title":
					title = attr.Val
				case "value":
					value = attr.Val
				}
			}
			isProductButton := hasClass(className, "add-product-detail") || hasClass(className, "js-add-to-cart-initial")
			if isProductButton && (isHebeAddToCartLabel(title) || isHebeAddToCartLabel(value) || isHebeAddToCartLabel(nodeText(n))) {
				found = true
				return
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	return found
}

func hasClass(className, target string) bool {
	for _, class := range strings.Fields(className) {
		if class == target {
			return true
		}
	}
	return false
}

func isHebeAddToCartLabel(value string) bool {
	return strings.EqualFold(strings.TrimSpace(value), "DODAJ DO KOSZYKA")
}

func nodeText(n *html.Node) string {
	var b strings.Builder
	var walk func(*html.Node)
	walk = func(current *html.Node) {
		if current.Type == html.TextNode {
			b.WriteString(current.Data)
		}
		for c := current.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return b.String()
}

// ParseHebePrice reads Hebe's analytics payload. JSON-LD can expose the regular
// price during a promotion, while data-product-gtm carries the actual current price.
func ParseHebePrice(body []byte) (HebePrice, error) {
	doc, err := html.Parse(strings.NewReader(string(body)))
	if err != nil {
		return HebePrice{}, fmt.Errorf("parse html: %w", err)
	}
	var result HebePrice
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if result.Current > 0 {
			return
		}
		if n.Type == html.ElementNode {
			for _, attr := range n.Attr {
				if attr.Key != "data-product-gtm" {
					continue
				}
				var payload struct {
					CurrentPrice float64 `json:"current_price"`
					RegularPrice float64 `json:"regular_price"`
					Currency     string  `json:"currency"`
				}
				if json.Unmarshal([]byte(attr.Val), &payload) == nil && payload.CurrentPrice > 0 {
					result = HebePrice{
						Current:  payload.CurrentPrice,
						Regular:  payload.RegularPrice,
						Currency: payload.Currency,
					}
					if result.Regular > result.Current {
						result.DiscountPercent = int(math.Round((result.Regular - result.Current) / result.Regular * 100))
					}
					return
				}
			}
		}
		for c := n.FirstChild; c != nil && result.Current == 0; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	if result.Current == 0 {
		return HebePrice{}, fmt.Errorf("no Hebe current_price found on page")
	}
	return result, nil
}
