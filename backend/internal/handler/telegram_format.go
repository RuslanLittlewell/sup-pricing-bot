package handler

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
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
	match := re.FindString(text)
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

func parseIntervalMinutes(text string) (int, bool) {
	re := regexp.MustCompile(`\d+`)
	match := re.FindString(text)
	if match == "" {
		return 0, false
	}
	minutes, err := strconv.Atoi(match)
	if err != nil {
		return 0, false
	}
	lower := strings.ToLower(text)
	if strings.Contains(lower, "час") || strings.Contains(lower, "hour") || strings.Contains(lower, "godz") {
		minutes *= 60
	}
	return minutes, minutes > 0
}

func formatInterval(lang string, minutes int) string {
	if minutes <= 0 {
		minutes = 180
	}
	if minutes%60 == 0 {
		hours := minutes / 60
		if hours == 1 {
			return tr(lang, "interval_every_hour")
		}
		return fmt.Sprintf(tr(lang, "interval_every_hours"), hours)
	}
	return fmt.Sprintf(tr(lang, "interval_every_minutes"), minutes)
}

func normalizePriceNumber(text string) string {
	normalized := strings.ReplaceAll(text, " ", "")
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
