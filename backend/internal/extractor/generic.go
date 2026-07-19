package extractor

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/littlewell/price-tracker/internal/shops"
	"golang.org/x/net/html"
)

type GenericExtractor struct{}

func NewGeneric() *GenericExtractor {
	return &GenericExtractor{}
}

func (e *GenericExtractor) Domain() string { return "*" }

func (e *GenericExtractor) Extract(htmlContent []byte, url string) (*ExtractionResult, error) {
	result := &ExtractionResult{
		Candidates: []PriceCandidate{},
	}
	if IsAllegroURL(url) {
		if allegro := extractAllegroOffer(htmlContent, url); allegro != nil {
			return allegro, nil
		}
		// Never run broad DOM/regex price scans over an Allegro challenge or partial
		// page: it contains short generated tokens such as "z1" and many unrelated
		// monetary values. Returning no candidates lets the caller use search fallback.
		return result, nil
	}

	doc, err := html.Parse(strings.NewReader(string(htmlContent)))
	if err != nil {
		return result, nil
	}

	if shops.IsHebeURL(url) {
		if hebePrice, hebeErr := shops.ParseHebePrice(htmlContent); hebeErr == nil {
			rule, _ := json.Marshal(map[string]string{"type": shops.HebePriceMethod, "field": "current_price"})
			result.Candidates = append(result.Candidates, PriceCandidate{
				Price: fmt.Sprintf("%.2f", hebePrice.Current), Currency: hebePrice.Currency,
				Confidence: 1, Label: "Hebe current promotional price", Rule: rule,
			})
			if hebePrice.Regular > hebePrice.Current {
				result.RegularPrice = fmt.Sprintf("%.2f", hebePrice.Regular)
				result.DiscountPercent = hebePrice.DiscountPercent
			}
		}
	}
	if shops.IsRossmannURL(url) {
		if rossmannPrice, rossmannErr := shops.ParseRossmannPrice(htmlContent); rossmannErr == nil {
			rule, _ := json.Marshal(map[string]string{"type": shops.RossmannPriceMethod, "field": `data-testid="product-price"`, "verified_by": "json_ld"})
			result.Candidates = append(result.Candidates, PriceCandidate{
				Price: fmt.Sprintf("%.2f", rossmannPrice.Current), Currency: rossmannPrice.Currency,
				Confidence: 1, Label: "Rossmann product price verified by JSON-LD", Rule: rule,
			})
		}
	}
	if shops.IsNikeURL(url) {
		if nikePrice, nikeErr := shops.ParseNikePrice(htmlContent, shops.NikeStyleColor(url)); nikeErr == nil {
			rule, _ := json.Marshal(map[string]string{"type": shops.NikePriceMethod, "field": "prices.currentPrice", "style_color": shops.NikeStyleColor(url)})
			result.Candidates = append(result.Candidates, PriceCandidate{
				Price: fmt.Sprintf("%.2f", nikePrice.Current), Currency: nikePrice.Currency,
				Confidence: 1, Label: "Nike selected style price", Rule: rule,
			})
			if nikePrice.Initial > nikePrice.Current {
				result.RegularPrice = fmt.Sprintf("%.2f", nikePrice.Initial)
				result.DiscountPercent = nikePrice.DiscountPercent
			}
		}
	}

	if ld := extractJSONLD(doc); ld != nil {
		result.Title = ld.Name
		result.ImageURL = ld.Image
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
	}

	metaTitle, metaImage, metaPrice, metaCurrency, metaAvail := extractMetaTags(doc)
	if result.Title == "" && metaTitle != "" {
		result.Title = metaTitle
	}
	if result.ImageURL == "" && metaImage != "" {
		result.ImageURL = metaImage
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
	if result.StockStatus == "unknown" && metaAvail != "" {
		result.StockStatus = mapAvailability(metaAvail)
	}

	// Schema.org microdata (itemprop attributes) — a third structured-data flavor next
	// to JSON-LD and OpenGraph meta tags. allegrolokalnie.pl, for one, marks up its offer
	// this way and emits neither of the other two.
	microName, microPrice, microCurrency, microAvail := extractMicrodata(doc)
	if result.Title == "" && microName != "" {
		result.Title = microName
	}
	if microPrice != "" {
		rule, _ := json.Marshal(map[string]string{"type": "microdata", "itemprop": "price"})
		result.Candidates = append(result.Candidates, PriceCandidate{
			Price:      microPrice,
			Currency:   microCurrency,
			Confidence: 0.9,
			Label:      "Microdata price",
			Rule:       rule,
		})
	}
	if (result.StockStatus == "" || result.StockStatus == "unknown") && microAvail != "" {
		result.StockStatus = mapAvailability(microAvail)
	}

	if len(result.Candidates) == 0 {
		candidates := extractPriceCandidates(doc)
		for i := range candidates {
			rule, _ := json.Marshal(map[string]string{"type": "css_selector", "selector": "common"})
			candidates[i].Rule = rule
		}
		result.Candidates = append(result.Candidates, candidates...)
	}

	if len(result.Candidates) == 0 {
		candidates := extractByRegex(string(htmlContent))
		result.Candidates = append(result.Candidates, candidates...)
	}

	return result, nil
}

func extractJSONLD(n *html.Node) *jsonLDProduct {
	if n.Type == html.ElementNode && n.Data == "script" {
		var isLD bool
		for _, attr := range n.Attr {
			if attr.Key == "type" && attr.Val == "application/ld+json" {
				isLD = true
				break
			}
		}
		if isLD && n.FirstChild != nil {
			var data interface{}
			if err := json.Unmarshal([]byte(n.FirstChild.Data), &data); err == nil {
				if obj, ok := data.(map[string]interface{}); ok {
					if p := parseLDObject(obj); p != nil {
						return p
					}
				}
				if arr, ok := data.([]interface{}); ok {
					for _, item := range arr {
						if obj, ok := item.(map[string]interface{}); ok {
							if p := parseLDObject(obj); p != nil {
								return p
							}
						}
					}
				}
			}
		}
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if p := extractJSONLD(c); p != nil {
			return p
		}
	}
	return nil
}

func parseLDObject(obj map[string]interface{}) *jsonLDProduct {
	ctx, _ := obj["@context"].(string)
	if !strings.Contains(ctx, "schema.org") {
		return nil
	}
	typ, _ := obj["@type"].(string)
	if !strings.Contains(typ, "Product") {
		return nil
	}

	p := &jsonLDProduct{}
	p.Name, _ = obj["name"].(string)
	p.Image, _ = obj["image"].(string)

	if offers, ok := obj["offers"].(map[string]interface{}); ok {
		p.Offers.Price, _ = offers["price"].(string)
		if p.Offers.Price == "" {
			if pr, ok := offers["price"].(float64); ok {
				p.Offers.Price = fmt.Sprintf("%.2f", pr)
			}
		}
		p.Offers.PriceCurrency, _ = offers["priceCurrency"].(string)
		p.Offers.Availability, _ = offers["availability"].(string)
	}

	if offersArr, ok := obj["offers"].([]interface{}); ok && len(offersArr) > 0 {
		if firstOffer, ok := offersArr[0].(map[string]interface{}); ok {
			if p.Offers.Price == "" {
				p.Offers.Price, _ = firstOffer["price"].(string)
				if p.Offers.Price == "" {
					if pr, ok := firstOffer["price"].(float64); ok {
						p.Offers.Price = fmt.Sprintf("%.2f", pr)
					}
				}
			}
			if p.Offers.PriceCurrency == "" {
				p.Offers.PriceCurrency, _ = firstOffer["priceCurrency"].(string)
			}
			if p.Offers.Availability == "" {
				p.Offers.Availability, _ = firstOffer["availability"].(string)
			}
		}
	}

	return p
}

func extractMetaTags(n *html.Node) (title, image, price, currency, availability string) {
	if n.Type == html.ElementNode && n.Data == "meta" {
		var property, content string
		for _, attr := range n.Attr {
			switch attr.Key {
			case "property", "name":
				property = attr.Val
			case "content":
				content = attr.Val
			}
		}
		switch property {
		case "og:title", "twitter:title":
			if title == "" {
				title = content
			}
		case "og:image", "twitter:image":
			if image == "" {
				image = content
			}
		case "product:price:amount", "og:price:amount":
			if price == "" {
				price = content
			}
		case "product:price:currency", "og:price:currency":
			if currency == "" {
				currency = content
			}
		case "product:availability", "og:availability":
			if availability == "" {
				availability = content
			}
		}
	}
	if n.Type == html.ElementNode && n.Data == "title" && n.FirstChild != nil {
		if title == "" {
			title = n.FirstChild.Data
		}
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		ct, ci, cp, cc, ca := extractMetaTags(c)
		if title == "" {
			title = ct
		}
		if image == "" {
			image = ci
		}
		if price == "" {
			price = cp
		}
		if currency == "" {
			currency = cc
		}
		if availability == "" {
			availability = ca
		}
	}
	return
}

// extractMicrodata pulls schema.org microdata (itemprop attributes) out of the page.
// Values live in the content attribute for meta-style tags, in href for link-style ones
// (e.g. availability pointing at http://schema.org/SoldOut). For name, only
// content-attribute occurrences are trusted: bare itemprop="name" elements also appear
// on breadcrumbs and seller blocks, where the value is arbitrary visible text.
func extractMicrodata(n *html.Node) (name, price, currency, availability string) {
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode {
			itemprop := getAttr(n, "itemprop")
			content := getAttr(n, "content")
			switch itemprop {
			case "price":
				if price == "" && content != "" {
					price = content
				}
			case "priceCurrency":
				if currency == "" && content != "" {
					currency = content
				}
			case "availability":
				if availability == "" {
					if content != "" {
						availability = content
					} else if href := getAttr(n, "href"); href != "" {
						availability = href
					}
				}
			case "name":
				if name == "" && content != "" {
					name = strings.TrimSpace(content)
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

func extractPriceCandidates(n *html.Node) []PriceCandidate {
	var candidates []PriceCandidate
	seen := map[string]bool{}

	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode {
			class := getAttr(n, "class")
			id := getAttr(n, "id")

			isPrice := false
			for _, keyword := range []string{"price", "cost", "amount", "sale-price", "current-price", "product-price"} {
				if strings.Contains(strings.ToLower(class), keyword) || strings.Contains(strings.ToLower(id), keyword) {
					isPrice = true
					break
				}
			}

			if isPrice && n.FirstChild != nil && n.FirstChild.Type == html.TextNode {
				text := strings.TrimSpace(n.FirstChild.Data)
				if price := extractPriceFromText(text); price != "" && !seen[price] {
					seen[price] = true
					candidates = append(candidates, PriceCandidate{
						Price:      price,
						Confidence: 0.6,
						Label:      "CSS selector (" + class + ")",
					})
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return candidates
}

func extractPriceFromText(text string) string {
	re := regexp.MustCompile(`(\d[\d\s,.]*(?:[.,]\d{2})?)`)
	matches := re.FindString(text)
	if matches != "" {
		matches = strings.TrimSpace(matches)
		if len(matches) > 2 {
			return matches
		}
	}
	return ""
}

func extractByRegex(htmlContent string) []PriceCandidate {
	var candidates []PriceCandidate

	jsonPrice := regexp.MustCompile(`"price"\s*:\s*"(\d+\.?\d*)"`)
	if matches := jsonPrice.FindStringSubmatch(htmlContent); len(matches) > 1 {
		rule, _ := json.Marshal(map[string]string{"type": "regex_json"})
		candidates = append(candidates, PriceCandidate{
			Price:      matches[1],
			Confidence: 0.4,
			Label:      "Regex JSON match",
			Rule:       rule,
		})
		// Some sites store this same "price" field in minor units (cents/grosze)
		// as a bare integer instead of a decimal amount — e.g. "22900" meaning
		// 229.00, not literally 22 900. We can't tell which encoding a given
		// site uses from the regex alone, so offer both readings as separate,
		// lower-confidence candidates rather than silently trusting the 100x-
		// too-large literal value.
		if !strings.Contains(matches[1], ".") && len(matches[1]) > 2 {
			minorUnits := matches[1][:len(matches[1])-2] + "." + matches[1][len(matches[1])-2:]
			minorRule, _ := json.Marshal(map[string]string{"type": "regex_json_minor_units"})
			candidates = append(candidates, PriceCandidate{
				Price:      minorUnits,
				Confidence: 0.35,
				Label:      "Regex JSON match (minor units)",
				Rule:       minorRule,
			})
		}
	}

	displayPrice := regexp.MustCompile(`([$€£₽zł]\s*\d[\d\s,.]*(?:[.,]\d{2})?)`)
	if matches := displayPrice.FindString(htmlContent); matches != "" {
		rule, _ := json.Marshal(map[string]string{"type": "regex_display_price"})
		candidates = append(candidates, PriceCandidate{
			Price:      strings.TrimSpace(matches),
			Confidence: 0.3,
			Label:      "Regex text match",
			Rule:       rule,
		})
	}

	trailingCurrencyPrice := regexp.MustCompile(`(\d[\d\s,.]*(?:[.,]\d{2})?\s*(?:zł|PLN|€|EUR|\$|USD|£|GBP|₽|RUB))`)
	if matches := trailingCurrencyPrice.FindString(htmlContent); matches != "" {
		rule, _ := json.Marshal(map[string]string{"type": "regex_trailing_currency"})
		candidates = append(candidates, PriceCandidate{
			Price:      strings.TrimSpace(matches),
			Confidence: 0.3,
			Label:      "Regex trailing currency match",
			Rule:       rule,
		})
	}

	return candidates
}

func getAttr(n *html.Node, key string) string {
	for _, attr := range n.Attr {
		if attr.Key == key {
			return attr.Val
		}
	}
	return ""
}
