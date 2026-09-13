package shops

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
)

// EtisalatStockMethod reports stock read from the e& (Etisalat) eShop SKU endpoint.
const EtisalatStockMethod = "etisalat_sku_api"

// EtisalatSku is the part of the eShop getDeviceSku response the bot relies on.
type EtisalatSku struct {
	SkuID       string
	DisplayName string
	InStock     bool
}

func IsEtisalatURL(rawURL string) bool {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	host := strings.ToLower(strings.TrimSuffix(parsed.Hostname(), "."))
	return host == "etisalat.ae" || strings.HasSuffix(host, ".etisalat.ae")
}

// EtisalatSkuAPIURL returns the JSON endpoint the eShop's AngularJS app calls for the
// selected SKU (deviceConfigurationService.getDeviceSkubyId). The product page itself is a
// client-rendered shell with no product or stock data, so the keyword scan reads it as
// in_stock regardless of what the page shows once rendered.
func EtisalatSkuAPIURL(rawURL string) (string, error) {
	if !IsEtisalatURL(rawURL) {
		return "", fmt.Errorf("not an Etisalat URL")
	}
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return "", err
	}
	skuID := strings.TrimSpace(parsed.Query().Get("skuId"))
	if skuID == "" {
		return "", fmt.Errorf("etisalat skuId not found in URL")
	}
	return "https://" + parsed.Host + "/b2c/eshop/getDeviceSku/" + url.PathEscape(skuID), nil
}

// ParseEtisalatSku reads a getDeviceSku response. The inStock flag must be present: the
// endpoint is the only stock signal for these pages, so a body without it (an error
// payload, an HTML shell) is a failed lookup rather than an implicit in_stock.
func ParseEtisalatSku(body []byte) (EtisalatSku, error) {
	var payload struct {
		SkuID       string `json:"skuId"`
		DisplayName string `json:"displayName"`
		InStock     *bool  `json:"inStock"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return EtisalatSku{}, fmt.Errorf("parse Etisalat SKU JSON: %w", err)
	}
	if payload.SkuID == "" || payload.InStock == nil {
		return EtisalatSku{}, fmt.Errorf("etisalat SKU response has no skuId/inStock")
	}
	return EtisalatSku{SkuID: payload.SkuID, DisplayName: strings.TrimSpace(payload.DisplayName), InStock: *payload.InStock}, nil
}
