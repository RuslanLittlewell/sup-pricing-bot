package extractor

import "strings"

// DetectStockStatusFromText does a plain case-insensitive search for "out of stock" on the
// raw page body. Used by the availability-only tracking mode, which doesn't try to parse a price.
func DetectStockStatusFromText(htmlContent []byte) string {
	if strings.Contains(strings.ToLower(string(htmlContent)), "out of stock") {
		return "out_of_stock"
	}
	return "in_stock"
}
