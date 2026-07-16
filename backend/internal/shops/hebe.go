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
