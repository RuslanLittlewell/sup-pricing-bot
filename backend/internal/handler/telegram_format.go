package handler

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

func extractDomain(url string) string {
	url = strings.TrimPrefix(url, "https://")
	url = strings.TrimPrefix(url, "http://")
	parts := strings.Split(url, "/")
	if len(parts) > 0 {
		return parts[0]
	}
	return url
}

func isURL(text string) bool {
	return strings.HasPrefix(text, "https://") || strings.HasPrefix(text, "http://")
}

func parsePriceInput(text string) (float64, string, bool) {
	re := regexp.MustCompile(`\d+(?:[\s.,]\d+)*`)
	normalizedText := normalizeUnicodeSpaces(text)
	match := re.FindString(normalizedText)
	if match == "" {
		return 0, "PLN", false
	}
	normalized := normalizePriceNumber(match)
	price, err := strconv.ParseFloat(normalized, 64)
	return price, detectCurrency(text), err == nil && price > 0
}

func formatPriceForSearch(price float64) string {
	if price == float64(int64(price)) {
		return fmt.Sprintf("%.0f", price)
	}
	return fmt.Sprintf("%.2f", price)
}

func formatMoney(price float64) string {
	return fmt.Sprintf("💰 %.2f", price)
}

func normalizePriceNumber(text string) string {
	normalized := strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return -1
		}
		return r
	}, text)
	if strings.Contains(normalized, ",") && strings.Contains(normalized, ".") {
		if strings.LastIndex(normalized, ",") > strings.LastIndex(normalized, ".") {
			normalized = strings.ReplaceAll(normalized, ".", "")
			normalized = strings.ReplaceAll(normalized, ",", ".")
		} else {
			normalized = strings.ReplaceAll(normalized, ",", "")
		}
		return normalized
	}
	if strings.Count(normalized, ",") == 1 {
		return strings.ReplaceAll(normalized, ",", ".")
	}
	return strings.ReplaceAll(normalized, ",", "")
}

func normalizeUnicodeSpaces(text string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return ' '
		}
		return r
	}, text)
}

func detectCurrency(text string) string {
	lower := strings.ToLower(text)
	switch {
	case strings.Contains(lower, "zł"), strings.Contains(lower, "pln"):
		return "PLN"
	case strings.Contains(lower, "€"), strings.Contains(lower, "eur"):
		return "EUR"
	case strings.Contains(lower, "$"), strings.Contains(lower, "usd"):
		return "USD"
	case strings.Contains(lower, "£"), strings.Contains(lower, "gbp"):
		return "GBP"
	case strings.Contains(lower, "₽"), strings.Contains(lower, "rub"):
		return "RUB"
	default:
		return "PLN"
	}
}

func truncate(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n]) + "..."
}
