package extractor

import (
	"encoding/json"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"unicode"

	xhtml "golang.org/x/net/html"
)

// Allegro product pages contain many unrelated monetary values (delivery, instalments,
// promoted offers). dataLayer identifies the selected offer explicitly, so matching its
// idItem against the URL's offerId is substantially safer than scanning visible prices.
var allegroDataLayerRE = regexp.MustCompile(`(?s)dataLayer\s*=\s*(\[.*?\])\s*</script>`)

type allegroDataLayerItem struct {
	IDItem   string  `json:"idItem"`
	Name     string  `json:"offerName"`
	Price    float64 `json:"price"`
	Currency string  `json:"currency"`
	Active   string  `json:"active"`
}

func extractAllegroOffer(htmlContent []byte, rawURL string) *ExtractionResult {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return nil
	}
	if !IsAllegroURL(rawURL) {
		return nil
	}
	offerID := parsed.Query().Get("offerId")
	if offerID == "" {
		return nil
	}

	for _, match := range allegroDataLayerRE.FindAllSubmatch(htmlContent, -1) {
		if len(match) < 2 {
			continue
		}
		var items []allegroDataLayerItem
		if err := json.Unmarshal(match[1], &items); err != nil {
			continue
		}
		for _, item := range items {
			if item.IDItem != offerID || item.Price <= 0 {
				continue
			}
			currency := item.Currency
			if currency == "" {
				currency = "PLN"
			}
			rule, _ := json.Marshal(map[string]string{
				"type":     "allegro_offer",
				"offer_id": offerID,
			})
			stock := "in_stock"
			if strings.EqualFold(item.Active, "no") {
				stock = "out_of_stock"
			}
			return &ExtractionResult{
				Title:       item.Name,
				StockStatus: stock,
				Candidates: []PriceCandidate{{
					Price:      formatDecimalPrice(item.Price),
					Currency:   currency,
					Confidence: 1,
					Label:      "Allegro selected offer price",
					Rule:       rule,
				}},
			}
		}
	}

	// Allegro also serializes each UI box as application/json. This survives DOM
	// rendering more reliably than dataLayer on some variants of the product page.
	if result := extractAllegroSerializedOffer(htmlContent, offerID); result != nil {
		return result
	}
	return nil
}

// IsAllegroURL lets generic extractors avoid treating arbitrary challenge-page tokens
// as prices. Allegro must be resolved by an offer-aware parser or an external fallback.
func IsAllegroURL(rawURL string) bool {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	host := strings.ToLower(parsed.Hostname())
	return host == "allegro.pl" || strings.HasSuffix(host, ".allegro.pl")
}

func extractAllegroSerializedOffer(htmlContent []byte, offerID string) *ExtractionResult {
	doc, err := xhtml.Parse(strings.NewReader(string(htmlContent)))
	if err != nil {
		return nil
	}
	var found *ExtractionResult
	var walk func(*xhtml.Node)
	walk = func(node *xhtml.Node) {
		if found != nil {
			return
		}
		if node.Type == xhtml.ElementNode && node.Data == "script" && node.FirstChild != nil {
			var scriptType string
			for _, attr := range node.Attr {
				if attr.Key == "type" {
					scriptType = attr.Val
					break
				}
			}
			if scriptType == "application/json" {
				var value interface{}
				if json.Unmarshal([]byte(node.FirstChild.Data), &value) == nil {
					found = findAllegroOfferObject(value, offerID)
				}
			}
		}
		for child := node.FirstChild; child != nil && found == nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(doc)
	return found
}

func findAllegroOfferObject(value interface{}, offerID string) *ExtractionResult {
	switch value := value.(type) {
	case map[string]interface{}:
		id, _ := value["offerId"].(string)
		if id == offerID {
			if result := allegroResultFromObject(value, offerID); result != nil {
				return result
			}
		}
		for _, child := range value {
			if result := findAllegroOfferObject(child, offerID); result != nil {
				return result
			}
		}
	case []interface{}:
		for _, child := range value {
			if result := findAllegroOfferObject(child, offerID); result != nil {
				return result
			}
		}
	}
	return nil
}

func allegroResultFromObject(object map[string]interface{}, offerID string) *ExtractionResult {
	priceObject, _ := object["price"].(map[string]interface{})
	priceString, _ := priceObject["priceString"].(string)
	price, ok := parseAllegroPrice(priceString)
	if !ok {
		return nil
	}
	title, _ := object["name"].(string)
	if title == "" {
		title, _ = object["offerTitle"].(string)
	}
	rule, _ := json.Marshal(map[string]string{"type": "allegro_offer", "offer_id": offerID})
	return &ExtractionResult{
		Title:       title,
		StockStatus: "in_stock",
		Candidates: []PriceCandidate{{
			Price:      formatDecimalPrice(price),
			Currency:   "PLN",
			Confidence: 1,
			Label:      "Allegro selected offer price",
			Rule:       rule,
		}},
	}
}

func parseAllegroPrice(text string) (float64, bool) {
	numberRE := regexp.MustCompile(`\d[\d\s\x{00A0}.,]*`)
	number := numberRE.FindString(text)
	if number == "" {
		return 0, false
	}
	number = strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return -1
		}
		return r
	}, number)
	if strings.Contains(number, ",") {
		number = strings.ReplaceAll(number, ".", "")
		number = strings.ReplaceAll(number, ",", ".")
	}
	price, err := strconv.ParseFloat(number, 64)
	return price, err == nil && price > 0
}

func formatDecimalPrice(price float64) string {
	data, _ := json.Marshal(price)
	return string(data)
}
