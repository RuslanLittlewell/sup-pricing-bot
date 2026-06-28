package extractor

import (
	"encoding/json"
	"strings"

	"golang.org/x/net/html"
)

type ZaraExtractor struct{}

func NewZara() *ZaraExtractor {
	return &ZaraExtractor{}
}

func (e *ZaraExtractor) Domain() string { return "zara.com" }

func (e *ZaraExtractor) Extract(htmlContent []byte, url string) (*ExtractionResult, error) {
	result := &ExtractionResult{
		Candidates: []PriceCandidate{},
	}

	isZara := strings.Contains(url, "zara.com")

	doc, err := html.Parse(strings.NewReader(string(htmlContent)))
	if err != nil {
		return result, nil
	}

	// 1. Try JSON-LD first
	if ld := extractJSONLD(doc); ld != nil {
		result.Title = ld.Name
		if ld.Image != "" {
			result.ImageURL = ld.Image
		}
		if ld.Offers.Price != "" {
			rule, _ := json.Marshal(map[string]string{"type": "json_ld", "path": "offers.price"})
			result.Candidates = append(result.Candidates, PriceCandidate{
				Price:      ld.Offers.Price,
				Currency:   ld.Offers.PriceCurrency,
				Confidence: 0.95,
				Label:      "JSON-LD price",
				Rule:       rule,
			})
		}
		result.StockStatus = mapAvailability(ld.Offers.Availability)
		if result.Title != "" && len(result.Candidates) > 0 {
			return result, nil
		}
	}

	// 2. Try meta tags
	metaTitle, metaImage, metaPrice, metaCurrency, metaAvail := extractMetaTags(doc)
	if result.Title == "" {
		result.Title = metaTitle
	}
	if result.ImageURL == "" {
		result.ImageURL = metaImage
	}

	// 3. Zara-specific extraction (only for Zara URLs)
	if isZara {
		zaraPrice, zaraCurrency := extractZaraPrice(doc)
		if zaraPrice != "" {
			rule, _ := json.Marshal(map[string]string{"type": "zara_selector", "selector": ".product-detail-price"})
			result.Candidates = append(result.Candidates, PriceCandidate{
				Price:      zaraPrice,
				Currency:   zaraCurrency,
				Confidence: 0.8,
				Label:      "Zara price element",
				Rule:       rule,
			})
		}

		if result.StockStatus == "unknown" || result.StockStatus == "" {
			zaraStock := extractZaraStock(doc)
			if zaraStock != "" {
				result.StockStatus = zaraStock
			}
		}
	}

	if metaPrice != "" {
		rule, _ := json.Marshal(map[string]string{"type": "meta_tag", "property": "product:price:amount"})
		result.Candidates = append(result.Candidates, PriceCandidate{
			Price:      metaPrice,
			Currency:   metaCurrency,
			Confidence: 0.85,
			Label:      "Meta tag price",
			Rule:       rule,
		})
	}

	if result.StockStatus == "unknown" || result.StockStatus == "" {
		if metaAvail != "" {
			result.StockStatus = mapAvailability(metaAvail)
		}
	}

	// 4. Try common selectors as fallback
	if len(result.Candidates) == 0 {
		candidates := extractPriceCandidates(doc)
		for _, c := range candidates {
			rule, _ := json.Marshal(map[string]string{"type": "css_selector", "selector": "common"})
			c.Rule = rule
			c.Confidence = 0.5
		}
		result.Candidates = append(result.Candidates, candidates...)
	}

	return result, nil
}

func extractZaraPrice(n *html.Node) (price, currency string) {
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode {
			class := getAttr(n, "class")
			dataAttr := getAttr(n, "data-price")
			itemprop := getAttr(n, "itemprop")

			if dataAttr != "" {
				price = dataAttr
			}

			if itemprop == "price" && n.FirstChild != nil {
				price = strings.TrimSpace(n.FirstChild.Data)
			}

			if itemprop == "priceCurrency" && n.FirstChild != nil {
				currency = strings.TrimSpace(n.FirstChild.Data)
			}

			if strings.Contains(class, "price") || strings.Contains(class, "Price") {
				if n.FirstChild != nil {
					text := strings.TrimSpace(n.FirstChild.Data)
					if extracted := extractPriceFromText(text); extracted != "" && price == "" {
						price = extracted
					}
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return
}

func extractZaraStock(n *html.Node) string {
	var result string
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if result != "" {
			return
		}
		if n.Type == html.ElementNode {
			class := getAttr(n, "class")
			dataAttr := getAttr(n, "data-stock-status")

			if dataAttr == "in_stock" || dataAttr == "true" {
				result = "in_stock"
				return
			}
			if dataAttr == "out_of_stock" || dataAttr == "false" {
				result = "out_of_stock"
				return
			}

			if strings.Contains(class, "out-of-stock") || strings.Contains(class, "outOfStock") {
				result = "out_of_stock"
				return
			}
			if strings.Contains(class, "in-stock") || strings.Contains(class, "inStock") {
				result = "in_stock"
				return
			}

			if n.FirstChild != nil && n.FirstChild.Type == html.TextNode {
				text := strings.ToLower(n.FirstChild.Data)
				if strings.Contains(text, "out of stock") || strings.Contains(text, "sold out") || strings.Contains(text, "нет в наличии") {
					result = "out_of_stock"
					return
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return result
}
