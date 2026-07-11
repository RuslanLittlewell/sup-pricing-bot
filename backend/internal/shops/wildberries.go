package shops

import (
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

var wildberriesArticleRe = regexp.MustCompile(`/catalog/(\d+)/detail\.aspx`)

func IsWildberriesURL(rawURL string) bool {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	host := strings.ToLower(parsed.Hostname())
	return host == "wildberries.ru" || host == "www.wildberries.ru" || host == "wildberries.by" || host == "www.wildberries.by"
}

func WildberriesArticle(rawURL string) string {
	if !IsWildberriesURL(rawURL) {
		return ""
	}
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	match := wildberriesArticleRe.FindStringSubmatch(parsed.Path)
	if len(match) != 2 {
		return ""
	}
	return match[1]
}

func WildberriesAPIURL(rawURL string) (string, error) {
	article := WildberriesArticle(rawURL)
	if article == "" {
		return "", fmt.Errorf("wildberries article not found in URL")
	}
	parsed, _ := url.Parse(rawURL)
	q := url.Values{"appType": {"1"}, "locale": {"ru"}, "nm": {article}}
	if strings.HasSuffix(strings.ToLower(parsed.Hostname()), ".by") {
		q.Set("curr", "byn")
		q.Set("dest", "-59202")
	} else {
		q.Set("curr", "rub")
		q.Set("dest", "-1257786")
	}
	return "https://card.wb.ru/cards/v4/detail?" + q.Encode(), nil
}

type WildberriesProduct struct {
	Article string
	Title   string
	Price   float64
	InStock bool
}

func ParseWildberriesProduct(body []byte, article string) (WildberriesProduct, error) {
	var payload struct {
		Products []struct {
			ID    int64  `json:"id"`
			Name  string `json:"name"`
			Brand string `json:"brand"`
			Sizes []struct {
				Stocks []struct {
					Qty int `json:"qty"`
				} `json:"stocks"`
				Price struct {
					Product   int64 `json:"product"`
					Logistics int64 `json:"logistics"`
				} `json:"price"`
			} `json:"sizes"`
		} `json:"products"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return WildberriesProduct{}, fmt.Errorf("parse wildberries API: %w", err)
	}
	for _, product := range payload.Products {
		if fmt.Sprint(product.ID) != article {
			continue
		}
		out := WildberriesProduct{Article: article, Title: strings.TrimSpace(product.Brand + " " + product.Name)}
		var lowest int64
		for _, size := range product.Sizes {
			qty := 0
			for _, stock := range size.Stocks {
				qty += stock.Qty
			}
			if qty <= 0 || size.Price.Product <= 0 {
				continue
			}
			out.InStock = true
			total := size.Price.Product + size.Price.Logistics
			if lowest == 0 || total < lowest {
				lowest = total
			}
		}
		if lowest == 0 {
			return out, nil
		}
		out.Price = float64(lowest) / 100
		return out, nil
	}
	return WildberriesProduct{}, fmt.Errorf("wildberries product %s not found", article)
}
